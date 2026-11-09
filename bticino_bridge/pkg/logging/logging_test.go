package logging

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "logging")
	if err != nil {
		panic(err)
	}
	allowedLogDirs = append(allowedLogDirs, filepath.Clean(os.TempDir())+string(os.PathSeparator))
	defaultLogFile = filepath.Join(dir, "default.log")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// Close exists for tests only: the bridge keeps its log open until it exits.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func configure(t *testing.T, logger *logrus.Logger, options Options) {
	t.Helper()
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	Configure(logger, options)
}

func TestParseSize(t *testing.T) {
	valid := map[string]int64{
		"2MB":    2 << 20,
		"512KB":  512 << 10,
		"1GB":    1 << 30,
		"2mb":    2 << 20,
		" 3 MB ": 3 << 20,
		"100B":   100,
		"4096":   4096,
	}
	for input, want := range valid {
		got, err := ParseSize(input)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"", "MB", "0MB", "-1MB", "1.5MB", "10TB", "abc", "99999999999GB"} {
		if got, err := ParseSize(input); err == nil {
			t.Errorf("ParseSize(%q) = %d, want an error", input, got)
		}
	}
}

func files(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sizes := map[string]int64{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		sizes[entry.Name()] = info.Size()
	}
	return sizes
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRotationKeepsFileCountAndTotalSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.log")
	w, err := NewRotatingWriter(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	var last string
	for i := 0; i < 500; i++ {
		last = fmt.Sprintf("line-%04d-xxxxxxxxxx\n", i)
		if n, err := w.Write([]byte(last)); err != nil || n != len(last) {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}

	sizes := files(t, dir)
	if len(sizes) != 3 {
		t.Fatalf("files = %v, want 3", sizes)
	}
	for name, size := range sizes {
		if size > 100 {
			t.Errorf("%s has %d bytes, limit 100", name, size)
		}
	}
	if !strings.HasSuffix(read(t, path), last) {
		t.Error("newest line is not in the active file")
	}
	// The rotated generations hold the lines just before the active file.
	if !strings.Contains(read(t, path+".1"), "line-049") {
		t.Errorf("unexpected .1 content: %q", read(t, path+".1"))
	}
}

func TestSingleFileIsEmptiedInsteadOfRotated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.log")
	w, err := NewRotatingWriter(path, 50, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for i := 0; i < 20; i++ {
		fmt.Fprintf(w, "line-%02d-xxxxxxxxxx\n", i)
	}
	sizes := files(t, dir)
	if len(sizes) != 1 || sizes["bridge.log"] > 50 {
		t.Fatalf("files = %v, want one file within the limit", sizes)
	}
}

func TestOversizedExistingFileRotatesOnFirstWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.log")
	old := strings.Repeat("o", 500)
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := NewRotatingWriter(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	fmt.Fprint(w, "new\n")
	if got := read(t, path); got != "new\n" {
		t.Errorf("active file = %q", got)
	}
	if got := read(t, path+".1"); got != old {
		t.Errorf("rotated file has %d bytes", len(got))
	}
}

func TestRestartAppendsToExistingLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge.log")
	first, err := NewRotatingWriter(path, 1000, 2)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(first, "before\n")
	first.Close()
	second, err := NewRotatingWriter(path, 1000, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	fmt.Fprint(second, "after\n")
	if got := read(t, path); got != "before\nafter\n" {
		t.Errorf("log = %q", got)
	}
}

func TestLoweredMaxFilesRemovesLeftovers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.log")
	for i := 1; i <= 4; i++ {
		if err := os.WriteFile(fmt.Sprintf("%s.%d", path, i), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w, err := NewRotatingWriter(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	sizes := files(t, dir)
	if len(sizes) != 2 {
		t.Fatalf("files = %v, want the log and .1", sizes)
	}
}

func TestWriteFailureDropsLinesAndRecovers(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "log")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "bridge.log")
	w, err := NewRotatingWriter(path, 20, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	clock := time.Unix(1000, 0)
	w.now = func() time.Time { return clock }

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	// The first rotation fails to reopen; all of these must still report success.
	for i := 0; i < 3; i++ {
		line := "a line longer than 20 bytes\n"
		if n, err := w.Write([]byte(line)); err != nil || n != len(line) {
			t.Fatalf("Write = %d, %v; a log failure must not reach the caller", n, err)
		}
	}
	if w.file != nil {
		t.Fatal("rotation into a removed directory should have closed the file")
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(w, "too early\n")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("reopened before the retry interval")
	}

	clock = clock.Add(reopenInterval)
	fmt.Fprint(w, "ok\n")
	got := read(t, path)
	if !strings.Contains(got, "lines dropped") || !strings.HasSuffix(got, "ok\n") {
		t.Errorf("log after recovery = %q", got)
	}
}

func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	w, err := NewRotatingWriter(filepath.Join(dir, "bridge.log"), 200, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var group sync.WaitGroup
	for g := 0; g < 8; g++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for i := 0; i < 200; i++ {
				fmt.Fprint(w, "concurrent line\n")
			}
		}()
	}
	group.Wait()
	sizes := files(t, dir)
	if len(sizes) != 3 {
		t.Fatalf("files = %v, want 3", sizes)
	}
	for name, size := range sizes {
		if size > 200 {
			t.Errorf("%s has %d bytes, limit 200", name, size)
		}
	}
}

func TestTrimTailKeepsLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	var content bytes.Buffer
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&content, "line %03d\n", i)
	}
	if err := os.WriteFile(path, content.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := trimTail(path, 500, 50); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.HasPrefix(got, "line ") || !strings.HasSuffix(got, "line 099\n") || len(got) > 50 {
		t.Errorf("trimmed file = %q", got)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("temporary file left behind")
	}
}

func TestTrimTailLeavesSmallOrMissingFileAlone(t *testing.T) {
	dir := t.TempDir()
	if err := trimTail(filepath.Join(dir, "missing.log"), 100, 50); err != nil {
		t.Errorf("missing file: %v", err)
	}
	path := filepath.Join(dir, "crash.log")
	if err := os.WriteFile(path, []byte("panic: boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := trimTail(path, 100, 50); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "panic: boom\n" {
		t.Errorf("file = %q", got)
	}
}

// A real crash can only be observed from outside, so the test runs itself again.
func TestCrashLogRecordsPanic(t *testing.T) {
	if path := os.Getenv("LOGGING_TEST_CRASH_FILE"); path != "" {
		if _, err := EnableCrashLog(path, "1.2.3"); err != nil {
			t.Fatal(err)
		}
		go panic("boom-from-test")
		time.Sleep(10 * time.Second)
		return
	}

	path := filepath.Join(t.TempDir(), "crash.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("old line\n", 200000)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashLogRecordsPanic$")
	cmd.Env = append(os.Environ(), "LOGGING_TEST_CRASH_FILE="+path)
	if err := cmd.Run(); err == nil {
		t.Fatal("the child should have crashed")
	}
	got := read(t, path)
	for _, want := range []string{"--- bridge 1.2.3 started", "panic: boom-from-test", "goroutine "} {
		if !strings.Contains(got, want) {
			t.Errorf("crash log misses %q", want)
		}
	}
	if len(got) > crashLimit {
		t.Errorf("old content was not trimmed: %d bytes", len(got))
	}
}

func TestEnableCrashLogReportsUnusablePath(t *testing.T) {
	if _, err := EnableCrashLog(filepath.Join(t.TempDir(), "missing", "crash.log"), "1.2.3"); err == nil {
		t.Error("expected an error")
	}
}

func TestLastCrash(t *testing.T) {
	const marker = "--- bridge 1.2.3 started 2026-01-01T00:00:00Z\n"
	cases := map[string]string{
		"":                                      "",
		marker:                                  "",
		marker + "--- 2026 log problem\n":       "",
		marker + "\npanic: boom\ngoroutine 1\n": "panic: boom",
		marker + "--- 2026 note\nfatal error: x\n":       "fatal error: x",
		marker + "panic: old\n" + marker:                 "",
		"panic: start of the marker was cut off\nmore\n": "panic: start of the marker was cut off",
	}
	for content, want := range cases {
		path := filepath.Join(t.TempDir(), "crash.log")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := lastCrash(path); got != want {
			t.Errorf("lastCrash(%q) = %q, want %q", content, got, want)
		}
	}
	if got := lastCrash(filepath.Join(t.TempDir(), "missing.log")); got != "" {
		t.Errorf("missing file: %q", got)
	}
}

func TestLogFileThatAppearsLaterIsUsedAndProblemIsNoted(t *testing.T) {
	base := t.TempDir()
	crash := filepath.Join(base, "crash.log")
	if err := os.WriteFile(crash, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	crashPath = crash
	defer func() { crashPath = "" }()

	dir := filepath.Join(base, "log")
	path := filepath.Join(dir, "bridge.log")
	logger := logrus.New()
	var fallback bytes.Buffer
	logger.SetOutput(&fallback)
	configure(t, logger, Options{Level: "info", File: path, MaxSize: "1MB", MaxFiles: 2})

	writer, ok := logger.Out.(*RotatingWriter)
	if !ok {
		t.Fatalf("output is %T", logger.Out)
	}
	clock := time.Now()
	writer.now = func() time.Time { return clock }
	if writer.LastError() == nil {
		t.Fatal("LastError should report the missing directory")
	}
	logger.Info("while missing")
	if !strings.Contains(fallback.String(), "while missing") || !strings.Contains(fallback.String(), "Cannot open log file") {
		t.Errorf("fallback = %q", fallback.String())
	}
	if !strings.Contains(read(t, crash), "log file problem") {
		t.Errorf("crash log = %q", read(t, crash))
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(reopenInterval + time.Second)
	logger.Info("after recovery")
	got := read(t, path)
	if !strings.Contains(got, "lines dropped") || !strings.Contains(got, "after recovery") {
		t.Errorf("log = %q", got)
	}
	if writer.LastError() != nil {
		t.Errorf("LastError after recovery = %v", writer.LastError())
	}
}

func TestLastingProblemIsNotedOnlyOncePerInterval(t *testing.T) {
	base := t.TempDir()
	crash := filepath.Join(base, "crash.log")
	if err := os.WriteFile(crash, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	crashPath = crash
	defer func() { crashPath = "" }()

	w, err := NewRotatingWriter(filepath.Join(base, "missing", "bridge.log"), 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	w.now = func() time.Time { return clock }
	for i := 0; i < 100; i++ {
		clock = clock.Add(reopenInterval)
		fmt.Fprint(w, "line\n")
	}
	if got := strings.Count(read(t, crash), "log file problem"); got != 1 {
		t.Errorf("noted %d times within %v", got, 100*reopenInterval)
	}
	clock = clock.Add(problemInterval)
	fmt.Fprint(w, "line\n")
	if got := strings.Count(read(t, crash), "log file problem"); got != 2 {
		t.Errorf("noted %d times after the interval", got)
	}
}

func TestConfigureAppliesOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge.log")
	logger := logrus.New()
	configure(t, logger, Options{Level: "warn", File: path, MaxSize: "1MB", MaxFiles: 2})
	if logger.GetLevel() != logrus.WarnLevel {
		t.Errorf("level = %v", logger.GetLevel())
	}
	logger.Info("hidden")
	logger.Warn("shown")
	got := read(t, path)
	if strings.Contains(got, "hidden") || !strings.Contains(got, "shown") {
		t.Errorf("log = %q", got)
	}
}

func TestConfigureFallsBackOnBadValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge.log")
	logger := logrus.New()
	configure(t, logger, Options{Level: "loud", File: path, MaxSize: "huge", MaxFiles: -1})
	if logger.GetLevel() != logrus.ErrorLevel {
		t.Errorf("level = %v", logger.GetLevel())
	}
	writer, ok := logger.Out.(*RotatingWriter)
	if !ok {
		t.Fatalf("output is %T", logger.Out)
	}
	if writer.maxSize != DefaultMaxSize || writer.maxFiles != DefaultMaxFiles {
		t.Errorf("limits = %d, %d", writer.maxSize, writer.maxFiles)
	}
	for _, field := range []string{"logging.level", "logging.max_size", "logging.max_files"} {
		if !strings.Contains(read(t, path), field) {
			t.Errorf("no warning for %s", field)
		}
	}
}

func TestLimitsAreCapped(t *testing.T) {
	cases := []struct {
		size  string
		files int
		want  int64
		ok    bool
	}{
		{"", 0, DefaultMaxSize, true},
		{"48MB", 1, 48 << 20, true},
		{"16MB", 3, 16 << 20, true},
		{"10MB", 5, DefaultMaxSize, false},
		{"100GB", 1, DefaultMaxSize, false},
		{"1KB", 16, 1 << 10, true},
		{"1KB", 17, DefaultMaxSize, false},
		{"1KB", 1 << 30, DefaultMaxSize, false},
		{"9000000000GB", 1 << 30, DefaultMaxSize, false},
	}
	for _, c := range cases {
		size, files, errs := resolveLimits(c.size, c.files)
		if size != c.want || (len(errs) == 0) != c.ok {
			t.Errorf("resolveLimits(%q, %d) = %d, %d, %v", c.size, c.files, size, files, errs)
		}
		if size*int64(files) > MaxTotalSize {
			t.Errorf("resolveLimits(%q, %d) allows %d bytes", c.size, c.files, size*int64(files))
		}
	}
}

func TestCheckFileRejectsPathsOutsideLogDirs(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"/home/bticino/cfg/extra/config.yaml",
		"/var/log/../../home/bticino/cfg/extra/bticino_bridge",
		"/tmp/../etc/passwd",
		"/var/logs/x.log",
		"relative.log",
		link,
		dir,
	} {
		if checkFile(bad) == nil {
			t.Errorf("checkFile(%q) accepted", bad)
		}
	}
	for _, good := range []string{"", "/var/log/bticino_bridge.log", "/tmp/bridge.log", filepath.Join(dir, "new.log")} {
		if err := checkFile(good); err != nil {
			t.Errorf("checkFile(%q) = %v", good, err)
		}
	}
}

func TestConfigureNeverTouchesForbiddenFile(t *testing.T) {
	outside, err := os.MkdirTemp(".", "outside")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)
	abs, err := filepath.Abs(outside)
	if err != nil {
		t.Fatal(err)
	}
	if checkFile(filepath.Join(abs, "config.yaml")) == nil {
		t.Skip("the test directory is inside an allowed log directory")
	}
	target := filepath.Join(abs, "config.yaml")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logger := logrus.New()
	logger.SetOutput(&bytes.Buffer{})
	configure(t, logger, Options{File: target, MaxSize: "1KB", MaxFiles: 2})
	for i := 0; i < 200; i++ {
		logger.Error("a line that would force a rotation")
	}
	if got := read(t, target); got != "keep\n" {
		t.Errorf("forbidden file changed: %q", got)
	}
	if _, err := os.Stat(target + ".1"); err == nil {
		t.Error("forbidden file was rotated")
	}
}

func TestValidate(t *testing.T) {
	if errs := Validate(Options{}); len(errs) != 0 {
		t.Errorf("unset options: %v", errs)
	}
	if errs := Validate(Options{Level: "info", File: "/var/log/b.log", MaxSize: "2MB", MaxFiles: 3}); len(errs) != 0 {
		t.Errorf("valid options: %v", errs)
	}
	if errs := Validate(Options{Level: "loud", File: "/etc/passwd", MaxSize: "huge", MaxFiles: -1}); len(errs) != 4 {
		t.Errorf("bad options gave %d errors: %v", len(errs), errs)
	}
}

func TestConfigureUsesErrorLevelWhenNothingIsSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge.log")
	logger := logrus.New()
	configure(t, logger, Options{File: path, MaxSize: "1MB", MaxFiles: 2})
	logger.Warn("hidden")
	logger.Error("shown")
	got := read(t, path)
	if logger.GetLevel() != logrus.ErrorLevel || strings.Contains(got, "hidden") || !strings.Contains(got, "shown") {
		t.Errorf("level %v, log %q", logger.GetLevel(), got)
	}
	if strings.Contains(got, "Invalid") {
		t.Errorf("an unset level is not invalid: %q", got)
	}
}
