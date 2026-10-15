package sip

import "testing"

func TestParseFlexisipConf(t *testing.T) {
	aliases, reg := parseFlexisipConf("[global]\naliases=abc.bs.iotleg.com\n[module::Registrar]\nreg-domains=abc.bs.iotleg.com\n")
	if aliases != "abc.bs.iotleg.com" || reg != "abc.bs.iotleg.com" {
		t.Fatalf("got %q %q", aliases, reg)
	}
	aliases, _ = parseFlexisipConf("aliases=first.example.com,second.example.com\n")
	if aliases != "first.example.com" {
		t.Fatalf("want first of list, got %q", aliases)
	}
	aliases, _ = parseFlexisipConf("# nothing here\n")
	if aliases != "" {
		t.Fatalf("want empty, got %q", aliases)
	}
}
