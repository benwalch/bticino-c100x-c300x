package openwebnet

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestIsSingleFrame(t *testing.T) {
	valid := []string{"*8*19*20##", "*#130**1*2##", "*#*1##", "*8*1#1#4#21*16##", "*1*0*11**##"}
	invalid := []string{
		"", "*", "##", "8*19*20##", "*8*19*20#", "*8*19*20##\n",
		"*1*1*11##*8*19*20##",
		"*'; reboot; echo '##",
		"*8*19*20## ",
		"*8*a*20##",
	}
	for _, f := range valid {
		if !IsSingleFrame(f) {
			t.Errorf("IsSingleFrame(%q) = false, want true", f)
		}
	}
	for _, f := range invalid {
		if IsSingleFrame(f) {
			t.Errorf("IsSingleFrame(%q) = true, want false", f)
		}
	}
}

// useTestServer points SendLocalFrame at a listener that records what one
// client sent until its half-close, then closes after holdOpen.
func useTestServer(t *testing.T, holdOpen time.Duration) (received <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	original := localCommandAddress
	localCommandAddress = ln.Addr().String()
	t.Cleanup(func() { localCommandAddress = original })

	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		data, _ := io.ReadAll(conn)
		got <- string(data)
		conn.Write([]byte("*#*1##"))
		time.Sleep(holdOpen)
	}()
	return got
}

func TestSendLocalFrameSendsFrameLikeNetcat(t *testing.T) {
	received := useTestServer(t, 0)

	if err := SendLocalFrame("*#130**1*2##"); err != nil {
		t.Fatalf("SendLocalFrame: %v", err)
	}
	if got := <-received; got != "*#130**1*2##\n" {
		t.Errorf("server received %q, want frame plus newline", got)
	}
}

func TestSendLocalFrameWaitsForServerClose(t *testing.T) {
	const holdOpen = 300 * time.Millisecond
	useTestServer(t, holdOpen)

	start := time.Now()
	if err := SendLocalFrame("*8*19*20##"); err != nil {
		t.Fatalf("SendLocalFrame: %v", err)
	}
	if elapsed := time.Since(start); elapsed < holdOpen {
		t.Errorf("returned after %v, before the server closed (%v)", elapsed, holdOpen)
	}
}

func TestSendLocalFrameRejectsBeforeConnecting(t *testing.T) {
	received := useTestServer(t, 0)

	for _, frame := range []string{"*'; reboot; echo '##", "*1*1*11##*8*19*20##"} {
		if err := SendLocalFrame(frame); err == nil {
			t.Errorf("SendLocalFrame(%q) succeeded, want error", frame)
		}
	}
	select {
	case got := <-received:
		t.Errorf("server got a connection with %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSendLocalFrameFailsWhenPortIsClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	original := localCommandAddress
	localCommandAddress = ln.Addr().String()
	t.Cleanup(func() { localCommandAddress = original })
	ln.Close()

	if err := SendLocalFrame("*8*19*20##"); err == nil {
		t.Error("SendLocalFrame succeeded without a listener, want error")
	}
}
