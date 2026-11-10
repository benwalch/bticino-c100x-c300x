package sip

import (
	"fmt"
	"strings"
)

// AnswererDialog holds the transaction of a received INVITE + the answerer's own to-tag.
type AnswererDialog struct {
	CallID      string
	FromHdr     string   // the original From of the INVITE (with its tag)
	ToHdr       string   // the original To (without our tag)
	CSeq        string   // p.ej. "20 INVITE"
	Via         []string // ALL Via headers, in order
	RecordRoute []string
	ToTag       string // our own tag added to To
}

// BuildAnswererRegister builds the TCP REGISTER of the answerer's AOR against the local Flexisip.
func BuildAnswererRegister(user, domain, localHost string, localPort, expires int, branch, callID, fromTag string, cseq int) string {
	aor := fmt.Sprintf("sip:%s@%s", user, domain)
	lines := []string{
		fmt.Sprintf("REGISTER sip:%s SIP/2.0", domain),
		fmt.Sprintf("Via: SIP/2.0/TCP %s:%d;alias;branch=%s;rport", localHost, localPort, branch),
		"Max-Forwards: 70",
		fmt.Sprintf("From: <%s>;tag=%s", aor, fromTag),
		fmt.Sprintf("To: <%s>", aor),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %d REGISTER", cseq),
		fmt.Sprintf("Contact: <sip:%s@%s:%d;transport=tcp>;+sip.instance=\"<urn:uuid:%s>\"", user, localHost, localPort, callID),
		fmt.Sprintf("Expires: %d", expires),
		"User-Agent: bticino-bridge-answerer",
		"Content-Length: 0",
		"", "",
	}
	return strings.Join(lines, "\r\n")
}

// BuildAnswererResponse builds a response to an INVITE (100/180/200/486/487). It copies all
// Via and Record-Route headers of the INVITE and adds our to-tag, Contact (if contact != "")
// and SDP (if sdp != "").
func BuildAnswererResponse(status, reason string, d AnswererDialog, sdp, contact string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SIP/2.0 %s %s\r\n", status, reason)
	for _, v := range d.Via {
		fmt.Fprintf(&b, "Via: %s\r\n", v)
	}
	for _, rr := range d.RecordRoute {
		fmt.Fprintf(&b, "Record-Route: %s\r\n", rr)
	}
	fmt.Fprintf(&b, "From: %s\r\n", d.FromHdr)
	fmt.Fprintf(&b, "To: %s;tag=%s\r\n", d.ToHdr, d.ToTag)
	fmt.Fprintf(&b, "Call-ID: %s\r\n", d.CallID)
	fmt.Fprintf(&b, "CSeq: %s\r\n", d.CSeq)
	if contact != "" {
		fmt.Fprintf(&b, "Contact: %s\r\n", contact)
	}
	if sdp != "" {
		b.WriteString("Content-Type: application/sdp\r\n")
		fmt.Fprintf(&b, "Content-Length: %d\r\n\r\n", len(sdp))
		b.WriteString(sdp)
	} else {
		b.WriteString("Content-Length: 0\r\n\r\n")
	}
	return b.String()
}

// BuildAnswererBYE builds the answerer's in-dialog BYE towards the entrance panel.
func BuildAnswererBYE(d AnswererDialog, user, domain, localHost string, localPort, cseq int, branch string) string {
	target := d.FromHdr // the INVITE sender is the BYE target
	if i := strings.Index(target, ";"); i >= 0 {
		target = target[:i]
	}
	target = strings.Trim(target, "<>")
	var b strings.Builder
	fmt.Fprintf(&b, "BYE %s SIP/2.0\r\n", target)
	fmt.Fprintf(&b, "Via: SIP/2.0/TCP %s:%d;alias;branch=%s;rport\r\n", localHost, localPort, branch)
	for _, rr := range d.RecordRoute { // reversed Route if there was a Record-Route
		fmt.Fprintf(&b, "Route: %s\r\n", rr)
	}
	b.WriteString("Max-Forwards: 70\r\n")
	// From/To are swapped relative to the incoming INVITE: our tag goes in From.
	fmt.Fprintf(&b, "From: %s;tag=%s\r\n", d.ToHdr, d.ToTag)
	fmt.Fprintf(&b, "To: %s\r\n", d.FromHdr)
	fmt.Fprintf(&b, "Call-ID: %s\r\n", d.CallID)
	fmt.Fprintf(&b, "CSeq: %d BYE\r\n", cseq)
	b.WriteString("Content-Length: 0\r\n\r\n")
	return b.String()
}
