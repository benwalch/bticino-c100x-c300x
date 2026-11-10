package sip

import (
	"crypto/rand"
	"encoding/base64"
)

// NewSRTPMasterKey generates the inline: value of an a=crypto line for the
// AES_CM_128_HMAC_SHA1_80 suite: 16 bytes of master key + 14 of master salt,
// in standard base64. Every call uses a new key.
func NewSRTPMasterKey() (string, error) {
	raw := make([]byte, 30)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}
