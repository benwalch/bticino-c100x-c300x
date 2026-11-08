package openwebnet

import (
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"bticino_bridge/pkg/bticino"
)

// localCommandAddress is what `nc 0 30006` dialed; Go treats 0.0.0.0 as the local system.
var localCommandAddress = fmt.Sprintf("0.0.0.0:%d", bticino.OpenWebNetLocalPort)

const (
	localDialTimeout  = 2 * time.Second
	localCloseTimeout = 5 * time.Second
)

// IsSingleFrame reports whether frame is exactly one OpenWebNet frame: only
// digits, '*' and '#', starting with '*' and ending with the only "##".
func IsSingleFrame(frame string) bool {
	if !strings.HasPrefix(frame, "*") || !strings.HasSuffix(frame, "##") || strings.Index(frame, "##") != len(frame)-2 {
		return false
	}
	return strings.Trim(frame, "0123456789*#") == ""
}

// SendLocalFrame sends one frame to the native command port in a fresh
// connection, the same exchange as `echo '<frame>' | nc 0 30006`: frame plus
// newline, half-close, then wait for the server to close. The reply is ignored.
func SendLocalFrame(frame string) error {
	if !IsSingleFrame(frame) {
		return fmt.Errorf("not a single OpenWebNet frame: %q", frame)
	}

	conn, err := net.DialTimeout("tcp", localCommandAddress, localDialTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(localCloseTimeout))
	if _, err := io.WriteString(conn, frame+"\n"); err != nil {
		return err
	}
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		return err
	}
	io.Copy(io.Discard, conn)
	return nil
}
