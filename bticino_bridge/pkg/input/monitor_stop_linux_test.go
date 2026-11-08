package input

import (
	"fmt"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/sirupsen/logrus"
)

// openPTYSlavePath returns the path of a pseudo terminal slave: a character
// device that blocks on read and supports poll, like /dev/input/event*.
func openPTYSlavePath(t *testing.T) string {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo terminals: %v", err)
	}
	t.Cleanup(func() { master.Close() })

	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Fatalf("unlockpt: %v", e)
	}
	var number uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&number))); e != 0 {
		t.Fatalf("ptsname: %v", e)
	}
	return fmt.Sprintf("/dev/pts/%d", number)
}

func TestStopEndsReadBlockedOnCharacterDevice(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	im := NewInputMonitor(logger)
	im.EventDevices = []string{openPTYSlavePath(t)}
	im.GPIOPins = nil

	if err := im.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForOpenFile(t, im)

	done := make(chan error, 1)
	go func() { done <- im.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return while a read was blocked")
	}
}

func waitForOpenFile(t *testing.T, im *InputMonitor) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		im.mutex.RLock()
		open := len(im.openFiles)
		im.mutex.RUnlock()
		if open > 0 {
			time.Sleep(50 * time.Millisecond) // let the reader block in Read
			return
		}
	}
	t.Fatal("device was never opened")
}
