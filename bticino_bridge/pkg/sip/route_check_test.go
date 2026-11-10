package sip

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckAnswererRoute(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "route_ext.conf")
	write := func(s string) { os.WriteFile(p, []byte(s), 0o644) }

	// Without bridge:
	write("<sip:alluser@YOUR_GATEWAY_ID.bs.iotleg.com> <sip:user1@YOUR_GATEWAY_ID.bs.iotleg.com>,<sip:user5@YOUR_GATEWAY_ID.bs.iotleg.com>\n")
	if ok, err := CheckAnswererRoute(p, "bridge", "YOUR_GATEWAY_ID.bs.iotleg.com"); err != nil || ok {
		t.Fatalf("expected (false,nil), got (%v,%v)", ok, err)
	}
	// With bridge:
	write("<sip:alluser@YOUR_GATEWAY_ID.bs.iotleg.com> <sip:user1@YOUR_GATEWAY_ID.bs.iotleg.com>,<sip:bridge@YOUR_GATEWAY_ID.bs.iotleg.com>\n")
	if ok, err := CheckAnswererRoute(p, "bridge", "YOUR_GATEWAY_ID.bs.iotleg.com"); err != nil || !ok {
		t.Fatalf("expected (true,nil), got (%v,%v)", ok, err)
	}
	// Missing file => error:
	if _, err := CheckAnswererRoute(filepath.Join(dir, "nope"), "bridge", "YOUR_GATEWAY_ID.bs.iotleg.com"); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
