package sip

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// rtpPkt is what the test reads from the relay's output.
type rtpPkt struct {
	pt      byte
	seq     uint16
	ts      uint32
	payload []byte
}

func listenOut(t *testing.T) (*net.UDPConn, int) {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen output: %v", err)
	}
	return c, c.LocalAddr().(*net.UDPAddr).Port
}

func readPkt(t *testing.T, c *net.UDPConn, timeout time.Duration) (rtpPkt, bool) {
	t.Helper()
	buf := make([]byte, 2048)
	c.SetReadDeadline(time.Now().Add(timeout))
	n, _, err := c.ReadFromUDP(buf)
	if err != nil || n < 12 {
		return rtpPkt{}, false
	}
	return rtpPkt{
		pt:      buf[1] & 0x7F,
		seq:     binary.BigEndian.Uint16(buf[2:4]),
		ts:      binary.BigEndian.Uint32(buf[4:8]),
		payload: append([]byte{}, buf[12:n]...),
	}, true
}

// micPacket builds a minimal RTP packet (12-byte header) like the one go2rtc sends.
func micPacket(seq uint16, payload []byte) []byte {
	h := make([]byte, 12)
	h[0] = 0x80
	h[1] = 97
	binary.BigEndian.PutUint16(h[2:4], seq)
	binary.BigEndian.PutUint32(h[8:12], 0xCAFEBABE)
	return append(h, payload...)
}

func sendMic(t *testing.T, port int, pkt []byte) {
	t.Helper()
	c, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatalf("dial mic: %v", err)
	}
	defer c.Close()
	c.Write(pkt)
}

// Without a microphone the relay emits continuous speex silence, with its own PT and a
// consecutive sequence: this keeps the upstream "alive" so the entrance panel opens the downstream audio.
func TestKeepalive_NoMic_ContinuousSilence(t *testing.T) {
	out, outPort := listenOut(t)
	defer out.Close()
	k, err := startUpstreamKeepalive("127.0.0.1", 0, outPort, 97, logrus.New())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer k.Stop()
	var prev *rtpPkt
	for i := 0; i < 6; i++ {
		p, ok := readPkt(t, out, time.Second)
		if !ok {
			t.Fatalf("packet %d: did not arrive (the relay must emit without input)", i)
		}
		if p.pt != 97 {
			t.Fatalf("PT=%d, expected 97", p.pt)
		}
		if !bytes.Equal(p.payload, speexSilenceFrame) {
			t.Fatalf("payload is not the silence frame: %x", p.payload)
		}
		if prev != nil && p.seq != prev.seq+1 {
			t.Fatalf("non-consecutive sequence: %d after %d", p.seq, prev.seq)
		}
		pp := p
		prev = &pp
	}
}

// When voice arrives from the HA microphone, its payload is forwarded as is, with no sequence gaps.
func TestKeepalive_ForwardsMicPayload(t *testing.T) {
	out, outPort := listenOut(t)
	defer out.Close()
	k, err := startUpstreamKeepalive("127.0.0.1", 0, outPort, 97, logrus.New())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer k.Stop()
	if _, ok := readPkt(t, out, time.Second); !ok { // already emitting silence
		t.Fatal("the relay did not start emitting")
	}
	voz := []byte("VOZ-DE-HA-0001")
	sendMic(t, k.InPort(), micPacket(500, voz))
	var prevSeq uint16
	have := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p, ok := readPkt(t, out, 300*time.Millisecond)
		if !ok {
			continue
		}
		if have && p.seq != prevSeq+1 {
			t.Fatalf("sequence gap: %d after %d", p.seq, prevSeq)
		}
		prevSeq, have = p.seq, true
		if bytes.Equal(p.payload, voz) {
			return // OK
		}
	}
	t.Fatal("the microphone payload was not forwarded")
}

// After the microphone goes silent, the relay inserts silence again.
func TestKeepalive_BackToSilenceAfterMic(t *testing.T) {
	out, outPort := listenOut(t)
	defer out.Close()
	k, err := startUpstreamKeepalive("127.0.0.1", 0, outPort, 97, logrus.New())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer k.Stop()
	voz := []byte("VOZ-DE-HA-0002")
	sendMic(t, k.InPort(), micPacket(1, voz))
	sawVoice := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p, ok := readPkt(t, out, 300*time.Millisecond)
		if !ok {
			continue
		}
		if bytes.Equal(p.payload, voz) {
			sawVoice = true
			continue
		}
		if sawVoice && bytes.Equal(p.payload, speexSilenceFrame) {
			return // OK: silence again after the voice
		}
	}
	t.Fatalf("silence did not come back after the voice (saw voice=%v)", sawVoice)
}

// Integration with a REAL ffmpeg (skipped if there is no ffmpeg in PATH): with the mic silent,
// ffmpeg reading from the relay (same arguments as in production) must output continuous SRTP.
// This is exactly the case that broke the naive amix design (ffmpeg hung without mic packets).
func TestKeepalive_IntegrationFFmpegOutputsSRTPWithoutMic(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	srtpOut, srtpPort := listenOut(t) // acts as the "entrance panel"
	defer srtpOut.Close()
	ffIn := freeUDPPort(t) // where ffmpeg listens (the relay's output)
	sdpFile := filepath.Join(t.TempDir(), "talk.sdp")
	if err := os.WriteFile(sdpFile, []byte(BuildBackchannelSDP(ffIn, 97)), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := GenerateSRTPKey()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ff, buildFFmpegArgs(sdpFile, key, "127.0.0.1", srtpPort, 96)...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("ffmpeg: %v", err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	k, err := startUpstreamKeepalive("127.0.0.1", 0, ffIn, 97, logrus.New())
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	defer k.Stop()
	got := 0
	deadline := time.Now().Add(6 * time.Second)
	for got < 25 && time.Now().Before(deadline) {
		if _, ok := readPkt(t, srtpOut, 500*time.Millisecond); ok {
			got++
		}
	}
	if got < 25 {
		t.Fatalf("ffmpeg only output %d SRTP packets without mic (expected a continuous stream)", got)
	}
}

// Stop frees the input port (the microphone's) and is idempotent.
func TestKeepalive_StopFreesPort(t *testing.T) {
	out, outPort := listenOut(t)
	defer out.Close()
	k, err := startUpstreamKeepalive("127.0.0.1", 0, outPort, 97, logrus.New())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	in := k.InPort()
	k.Stop()
	k.Stop()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: in})
	if err != nil {
		t.Fatalf("port %d still in use after Stop: %v", in, err)
	}
	c.Close()
}
