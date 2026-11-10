package sip

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ReadSIPFrame reads a complete SIP message from a TCP stream (headers up to CRLFCRLF +
// Content-Length bytes of body). It returns io.EOF when the connection closes.
func ReadSIPFrame(r *bufio.Reader) (*SIPMessage, error) {
	var head strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if head.Len() == 0 {
				return nil, io.EOF
			}
			return nil, err
		}
		head.WriteString(line)
		if line == "\r\n" || line == "\n" {
			break // end of headers
		}
	}
	raw := head.String()
	cl := contentLengthOf(raw)
	if cl > 0 {
		body := make([]byte, cl)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, fmt.Errorf("reading body (%d bytes): %w", cl, err)
		}
		raw += string(body)
	}
	return ParseSIPFrame(raw), nil
}

func contentLengthOf(raw string) int {
	for _, line := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			v := strings.TrimSpace(line[strings.IndexByte(line, ':')+1:])
			n, _ := strconv.Atoi(v)
			return n
		}
	}
	return 0
}

// ParseSIPFrame parses an already delimited SIP message. Via and Record-Route are collected in slices.
func ParseSIPFrame(raw string) *SIPMessage {
	msg := &SIPMessage{Headers: map[string]string{}, Raw: raw}
	parts := strings.SplitN(raw, "\r\n\r\n", 2)
	headerBlock := parts[0]
	if len(parts) == 2 {
		msg.Body = parts[1]
	}
	lines := strings.Split(headerBlock, "\r\n")
	if len(lines) > 0 {
		start := strings.Fields(lines[0])
		if strings.HasPrefix(lines[0], "SIP/2.0") && len(start) >= 2 {
			msg.Version, msg.StatusCode = start[0], start[1]
		} else if len(start) >= 3 {
			msg.Method, msg.RequestURI, msg.Version = start[0], start[1], start[2]
		}
	}
	for _, line := range lines[1:] {
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		switch strings.ToLower(k) {
		case "via":
			msg.ViaHeaders = append(msg.ViaHeaders, v)
		case "record-route":
			msg.RecordRoute = append(msg.RecordRoute, v)
		default:
			msg.Headers[k] = v
		}
	}
	return msg
}
