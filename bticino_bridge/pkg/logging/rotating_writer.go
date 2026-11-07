// Package logging keeps the bridge log within fixed limits. /var/log shares a
// tmpfs with /tmp and /var/run, so an unbounded log can starve the whole device.
package logging

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultMaxSize  int64 = 2 * 1024 * 1024
	DefaultMaxFiles       = 3

	reopenInterval = 5 * time.Second
	// The crash log is on flash, so a lasting problem must not write to it often.
	problemInterval = time.Hour
)

// ParseSize reads sizes like "2MB", "512KB", "1GB" or plain bytes.
func ParseSize(value string) (int64, error) {
	text := strings.ToUpper(strings.TrimSpace(value))
	unit := int64(1)
	for _, candidate := range []struct {
		suffix string
		factor int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(text, candidate.suffix) {
			text = strings.TrimSpace(strings.TrimSuffix(text, candidate.suffix))
			unit = candidate.factor
			break
		}
	}
	number, err := strconv.ParseInt(text, 10, 64)
	if err != nil || number <= 0 || number > (1<<62)/unit {
		return 0, fmt.Errorf("invalid log size %q", value)
	}
	return number * unit, nil
}

// RotatingWriter writes to path and keeps maxFiles files in total: path, path.1, ...
// Write never fails: a log problem must not affect the bridge. Lines that cannot be
// written go to fallback and are counted, and opening the file is retried, because
// its directory may appear later, for example during boot.
type RotatingWriter struct {
	mu          sync.Mutex
	path        string
	maxSize     int64
	maxFiles    int
	fallback    io.Writer
	file        *os.File
	size        int64
	dropped     int64
	retryAt     time.Time
	lastErr     error
	lastProblem time.Time
	now         func() time.Time
}

// NewRotatingWriter opens path for appending, so the log survives a bridge restart.
// A file that cannot be opened is no error here, see LastError.
func NewRotatingWriter(path string, maxSize int64, maxFiles int) (*RotatingWriter, error) {
	if maxSize <= 0 || maxFiles < 1 {
		return nil, fmt.Errorf("invalid log limits: size %d, files %d", maxSize, maxFiles)
	}
	w := &RotatingWriter{path: path, maxSize: maxSize, maxFiles: maxFiles, now: time.Now}
	w.retryOpen()
	// Files left over from a higher max_files would never rotate away.
	for i := maxFiles; os.Remove(w.rotated(i)) == nil; i++ {
	}
	return w, nil
}

// LastError returns the current problem with the log file, if there is one.
func (w *RotatingWriter) LastError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil && !w.retryOpen() {
		w.lose(p)
		return len(p), nil
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		w.rotate()
		if w.file == nil {
			w.lose(p)
			return len(p), nil
		}
	}
	if w.dropped > 0 {
		notice := fmt.Sprintf("%s logging: %d lines dropped\n", w.now().Format(time.RFC3339), w.dropped)
		if !w.writeOut([]byte(notice)) {
			w.lose(p)
			return len(p), nil
		}
		w.dropped = 0
	}
	if !w.writeOut(p) {
		w.lose(p)
	}
	return len(p), nil
}

func (w *RotatingWriter) lose(p []byte) {
	w.dropped++
	if w.fallback != nil {
		w.fallback.Write(p)
	}
}

func (w *RotatingWriter) writeOut(p []byte) bool {
	n, err := w.file.Write(p)
	w.size += int64(n)
	if err != nil {
		w.failed(err)
		return false
	}
	w.lastErr = nil
	return true
}

func (w *RotatingWriter) failed(err error) {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
	w.lastErr = err
	w.retryAt = w.now().Add(reopenInterval)
	if w.lastProblem.IsZero() || w.now().Sub(w.lastProblem) >= problemInterval {
		w.lastProblem = w.now()
		recordProblem(fmt.Sprintf("log file problem: %v", err))
	}
}

func (w *RotatingWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	w.file, w.size = file, info.Size()
	return nil
}

func (w *RotatingWriter) retryOpen() bool {
	if w.now().Before(w.retryAt) {
		return false
	}
	if err := w.open(); err != nil {
		w.failed(err)
		return false
	}
	return true
}

func (w *RotatingWriter) rotate() {
	w.file.Close()
	w.file = nil

	oldest := w.maxFiles - 1
	if oldest > 0 {
		os.Remove(w.rotated(oldest))
		for i := oldest - 1; i >= 1; i-- {
			os.Rename(w.rotated(i), w.rotated(i+1))
		}
	}
	// Without a rotated copy, emptying the file is what keeps the limit.
	if oldest == 0 || os.Rename(w.path, w.rotated(1)) != nil {
		os.Truncate(w.path, 0)
	}

	w.retryAt = time.Time{}
	w.retryOpen()
}

func (w *RotatingWriter) rotated(index int) string {
	return fmt.Sprintf("%s.%d", w.path, index)
}
