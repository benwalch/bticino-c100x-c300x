package system

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestRestartScriptWaitsForOldProcess(t *testing.T) {
	old := exec.Command("sleep", "30")
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	script := exec.Command("sh", "-c", restartScript, "bridge-restart",
		strconv.Itoa(old.Process.Pid), dir, "touch", marker)
	if err := script.Start(); err != nil {
		t.Fatal(err)
	}
	defer script.Process.Kill()

	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("replacement started while the old process was still running")
	}

	old.Process.Kill()
	old.Wait() // reaped, as the old bridge is by its parent
	if err := script.Wait(); err != nil {
		t.Fatalf("restart script: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("replacement did not start after the old process exited")
	}
}
