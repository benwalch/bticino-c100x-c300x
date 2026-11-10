package openwebnet

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"

	"bticino_bridge/pkg/bticino"
)

// commandSessionAddress is the native OpenWebNet server. Unlike 30006 it
// answers queries with data, and it passes frames on to the native processes
// (e.g. *8*99*## to bt_answering_machine).
var commandSessionAddress = fmt.Sprintf("127.0.0.1:%d", bticino.OpenWebNetMainPort)

const (
	ackFrame  = "*#*1##"
	nackFrame = "*#*0##"
)

// SendCommandSession opens a command session (*99*0##) on port 20000, sends
// one frame and returns the reply data: "" for a plain ACK, an error for NACK.
func SendCommandSession(command string) (string, error) {
	conn, err := net.DialTimeout("tcp", commandSessionAddress, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("error connecting to port %d: %w", bticino.OpenWebNetMainPort, err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	readMessage := func(timeout time.Duration) (string, error) {
		conn.SetReadDeadline(time.Now().Add(timeout))
		var msg strings.Builder
		for {
			b, err := reader.ReadByte()
			if err != nil {
				return msg.String(), err
			}
			msg.WriteByte(b)
			if s := msg.String(); strings.HasSuffix(s, "##") {
				return s, nil
			}
		}
	}

	greeting, err := readMessage(3 * time.Second)
	if err != nil {
		return "", fmt.Errorf("error reading greeting: %w", err)
	}
	if strings.Contains(greeting, "*98*") {
		return "", fmt.Errorf("server requires HMAC authentication, command session not supported")
	}

	if _, err := conn.Write([]byte("*99*0##")); err != nil {
		return "", fmt.Errorf("error sending command session: %w", err)
	}
	sessionResp, err := readMessage(3 * time.Second)
	if err != nil {
		return "", fmt.Errorf("error reading session response: %w", err)
	}
	if !strings.Contains(sessionResp, ackFrame) {
		return "", fmt.Errorf("command session rejected: %s", sessionResp)
	}

	if _, err := conn.Write([]byte(command)); err != nil {
		return "", fmt.Errorf("error sending command %s: %w", command, err)
	}
	firstResp, err := readMessage(3 * time.Second)
	if err != nil {
		return "", fmt.Errorf("error reading response to %s: %w", command, err)
	}
	switch firstResp {
	case nackFrame:
		return "", fmt.Errorf("command %s rejected (NACK)", command)
	case ackFrame:
		return "", nil
	}

	// Data comes first; the ACK that follows is read but not required.
	readMessage(2 * time.Second)
	return firstResp, nil
}
