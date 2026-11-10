package openwebnet

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// fakeOpenServer answers like the native server on port 20000: greeting ACK,
// ACK for *99*0##, then reply for the frame.
func fakeOpenServer(t *testing.T, reply string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	old := commandSessionAddress
	commandSessionAddress = ln.Addr().String()
	t.Cleanup(func() { ln.Close(); commandSessionAddress = old })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		readFrame := func() string {
			s, _ := r.ReadString('#')
			rest, _ := r.ReadString('#')
			return s + rest
		}
		conn.Write([]byte(ackFrame))
		if readFrame() != "*99*0##" {
			return
		}
		conn.Write([]byte(ackFrame))
		readFrame()
		conn.Write([]byte(reply))
	}()
}

func TestSendCommandSessionAck(t *testing.T) {
	fakeOpenServer(t, ackFrame)
	if data, err := SendCommandSession("*8*99*##"); err != nil || data != "" {
		t.Errorf("got %q, %v; want plain ACK", data, err)
	}
}

func TestSendCommandSessionNack(t *testing.T) {
	fakeOpenServer(t, nackFrame)
	if _, err := SendCommandSession("*8*99*##"); err == nil || !strings.Contains(err.Error(), "NACK") {
		t.Errorf("err = %v, want NACK error", err)
	}
}

func TestSendCommandSessionData(t *testing.T) {
	fakeOpenServer(t, "*#13**16*1*5*8##")
	if data, err := SendCommandSession("*#13**16##"); err != nil || data != "*#13**16*1*5*8##" {
		t.Errorf("got %q, %v", data, err)
	}
}
