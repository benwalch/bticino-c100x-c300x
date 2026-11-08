package logging

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// DefaultLevel applies when no level is configured.
const DefaultLevel = logrus.ErrorLevel

const (
	// MaxTotalSize limits max_size x max_files: the logs live in RAM.
	MaxTotalSize int64 = 48 * 1024 * 1024
	// MaxFilesLimit bounds the renames per rotation, which block all logging.
	MaxFilesLimit = 16
)

var (
	// The bridge runs as root and renames its log file, so logging.file must never
	// be able to reach the binary, the config or other files on the flash.
	allowedLogDirs = []string{"/var/log/", "/tmp/"}
	// defaultLogFile replaces a logging.file that is not allowed.
	defaultLogFile = "/var/log/bticino_bridge.log"

	crashMu   sync.Mutex
	crashPath string
)

const (
	crashLimit  = 1024 * 1024
	crashKeep   = 256 * 1024
	notePrefix  = "--- "
	startMarker = notePrefix + "bridge "
)

// Options mirrors the logging block of the config.
type Options struct {
	Level    string
	File     string
	MaxSize  string
	MaxFiles int
}

// Validate reports every option that Configure would replace. Unset values are valid.
func Validate(options Options) []error {
	var errs []error
	if options.Level != "" {
		if _, err := logrus.ParseLevel(options.Level); err != nil {
			errs = append(errs, fmt.Errorf("invalid logging.level %q", options.Level))
		}
	}
	if err := checkFile(options.File); err != nil {
		errs = append(errs, err)
	}
	_, _, limitErrs := resolveLimits(options.MaxSize, options.MaxFiles)
	return append(errs, limitErrs...)
}

// Configure applies the options. Bad values fall back to safe defaults, so a
// config typo can neither stop the bridge nor remove the size limit.
// An empty File keeps logging on stderr.
func Configure(logger *logrus.Logger, options Options) {
	level, err := logrus.ParseLevel(options.Level)
	if err != nil {
		level = DefaultLevel
	}
	logger.SetLevel(level)

	maxSize, maxFiles, _ := resolveLimits(options.MaxSize, options.MaxFiles)
	file := options.File
	if checkFile(file) != nil {
		file = defaultLogFile
	}

	var openErr error
	if file != "" {
		var writer *RotatingWriter
		writer, openErr = NewRotatingWriter(file, maxSize, maxFiles)
		if openErr == nil {
			writer.fallback = logger.Out
			logger.SetOutput(writer)
			// net/http and other libraries report through the standard logger.
			log.SetOutput(writer)
			openErr = writer.LastError()
		}
	}

	// Error level: these must stay visible at the default level.
	for _, problem := range Validate(options) {
		logger.Errorf("%v, using the default", problem)
	}
	if openErr != nil {
		logger.Errorf("Cannot open log file, retrying: %v", openErr)
	}
}

// resolveLimits returns usable limits and the problems found. A limit set too
// high resets both values, because only their product is known to be wrong.
func resolveLimits(maxSize string, maxFiles int) (int64, int, []error) {
	var errs []error
	size := DefaultMaxSize
	if maxSize != "" {
		parsed, err := ParseSize(maxSize)
		if err != nil {
			errs = append(errs, fmt.Errorf("invalid logging.max_size %q", maxSize))
		} else {
			size = parsed
		}
	}
	files := DefaultMaxFiles
	if maxFiles < 0 {
		errs = append(errs, fmt.Errorf("invalid logging.max_files %d", maxFiles))
	} else if maxFiles > MaxFilesLimit {
		errs = append(errs, fmt.Errorf("logging.max_files %d exceeds %d", maxFiles, MaxFilesLimit))
		return DefaultMaxSize, DefaultMaxFiles, errs
	} else if maxFiles > 0 {
		files = maxFiles
	}
	if size > MaxTotalSize/int64(files) {
		errs = append(errs, fmt.Errorf("logging.max_size x logging.max_files exceeds %d MB", MaxTotalSize>>20))
		return DefaultMaxSize, DefaultMaxFiles, errs
	}
	return size, files, errs
}

func checkFile(path string) error {
	if path == "" {
		return nil
	}
	clean := filepath.Clean(path)
	for _, dir := range allowedLogDirs {
		if strings.HasPrefix(clean, dir) {
			// Lstat: a symlink could lead out of the allowed directories.
			if info, err := os.Lstat(clean); err == nil && !info.Mode().IsRegular() {
				return fmt.Errorf("invalid logging.file %q: not a regular file", path)
			}
			return nil
		}
	}
	return fmt.Errorf("invalid logging.file %q: must be in %s", path, strings.Join(allowedLogDirs, " or "))
}

// EnableCrashLog sends Go crashes such as panics to path as well. The bridge starts
// with its output on /dev/null, so this file is the only record of a crash. It may
// live on flash: it is written once per start and on a crash, and survives a reboot.
// It returns the first line of a crash in the previous run, if there was one.
func EnableCrashLog(path, version string) (string, error) {
	trimErr := trimTail(path, crashLimit, crashKeep)
	previous := lastCrash(path)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return previous, errors.Join(trimErr, err)
	}
	defer file.Close()

	crashMu.Lock()
	crashPath = path
	crashMu.Unlock()

	// Crash traces carry no time or version, so each start leaves a marker.
	_, headerErr := fmt.Fprintf(file, "%s%s started %s\n", startMarker, version, time.Now().Format(time.RFC3339))
	return previous, errors.Join(trimErr, headerErr, debug.SetCrashOutput(file, debug.CrashOptions{}))
}

// lastCrash looks at what follows the last start marker. Own notes start with
// "--- ", so anything else there comes from the Go runtime.
func lastCrash(path string) string {
	tail, err := readTail(path, crashKeep)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(tail), "\n")
	start := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], startMarker) {
			start = i + 1
			break
		}
	}
	for _, line := range lines[start:] {
		if line != "" && !strings.HasPrefix(line, notePrefix) {
			return line
		}
	}
	return ""
}

// RecordProblem notes a problem in the crash log, where it can still be read when
// the log itself fails or the bridge exits before logging is set up.
func RecordProblem(message string) {
	crashMu.Lock()
	defer crashMu.Unlock()
	if crashPath == "" {
		return
	}
	file, err := os.OpenFile(crashPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "%s%s %s\n", notePrefix, time.Now().Format(time.RFC3339), message)
}

// trimTail cuts path to its last keep bytes once it exceeds limit. It goes through
// a new file, so a power loss leaves either the old or the new version.
func trimTail(path string, limit, keep int64) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Size() <= limit {
		return err
	}
	tail, err := readTail(path, keep)
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, tail, 0o644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func readTail(path string, keep int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	start := info.Size() - keep
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	tail, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	if start > 0 {
		// Start at a full line.
		for i, b := range tail {
			if b == '\n' {
				return tail[i+1:], nil
			}
		}
	}
	return tail, nil
}
