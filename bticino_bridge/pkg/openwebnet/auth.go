// HMAC-SHA256 authentication for OpenWebNet (BTicino Classe 300X protocol).
//
// Implemented from the analysis of slyoldfox/c300x-controller (lib/openwebnet.js).
//
// Authentication protocol:
//  1. Client sends *99*0## (COMMAND session request)
//  2. Server replies *98*2## (indicates HMAC-SHA256 auth is required)
//     - On localhost, it may reply *#*1## directly (no auth)
//  3. Client replies *#*1## (accepts HMAC)
//  4. Server sends *#NONCE## (nonce Ra encoded in OWN digits)
//  5. Client computes:
//     - Ra = digitToHex(server nonce)
//     - Rb = SHA256("time" + timestamp) (client-generated nonce)
//     - Kab = SHA256(password)
//     - HMAC = SHA256(Ra + Rb + A + B + Kab)
//     where A="736F70653E", B="636F70653E"
//  6. Client sends: *#<hexToDigit(Rb)>*<hexToDigit(HMAC)>##
//  7. Server sends its own HMAC (server-side validation)
//  8. Client replies *#*1## (accepts)
//  9. Server confirms with *#*1## -> authenticated
package openwebnet

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Fixed constants of the BTicino HMAC-SHA256 protocol
const (
	// Fixed A and B values for the HMAC computation.
	// Decoded: A = "sope>", B = "cope>"
	HMAC_A = "736F70653E"
	HMAC_B = "636F70653E"

	// OpenWebNet authentication commands
	CMD_SESSION_COMMAND = "*99*0##" // Request command session
	CMD_SESSION_EVENT   = "*99*1##" // Request event session
	CMD_AUTH_HMAC       = "*98*2##" // Server requires HMAC-SHA256 auth
	CMD_ACK             = "*#*1##"  // Acknowledgment
	CMD_NACK            = "*#*0##"  // Negative acknowledgment
)

// digitToHex converts an OWN digit string to hexadecimal.
// Each group of 4 decimal digits encodes 2 hex characters.
// Example: "0709" -> hex: (07=07, 09=09) -> "0709"
// Clearer: digits[0]*10 + digits[1] -> first hex nibble,
//
//	digits[2]*10 + digits[3] -> second hex nibble.
//
// Ex: "0100" -> (0*10+1=1 -> "1") + (0*10+0=0 -> "0") -> "10"
func digitToHex(digits string) string {
	var out strings.Builder
	chars := []rune(digits)

	for i := 0; i+3 < len(chars); i += 4 {
		d0, _ := strconv.Atoi(string(chars[i]))
		d1, _ := strconv.Atoi(string(chars[i+1]))
		d2, _ := strconv.Atoi(string(chars[i+2]))
		d3, _ := strconv.Atoi(string(chars[i+3]))

		// Each digit pair encodes a 0-15 value (one hex nibble)
		val1 := d0*10 + d1
		val2 := d2*10 + d3

		out.WriteString(fmt.Sprintf("%x%x", val1, val2))
	}

	return out.String()
}

// hexToDigit converts a hexadecimal string to OWN digits.
// Each hex char (0-f, value 0-15) is encoded as 2 decimal digits.
// Ex: "a" (value 10) -> "10", "f" (value 15) -> "15", "0" -> "00"
func hexToDigit(hexString string) string {
	var out strings.Builder

	for _, c := range hexString {
		hexValue, err := strconv.ParseInt(string(c), 16, 64)
		if err != nil {
			out.WriteString("00")
			continue
		}
		if hexValue < 10 {
			out.WriteByte('0')
		}
		out.WriteString(strconv.FormatInt(hexValue, 10))
	}

	return out.String()
}

// sha256Hex computes SHA-256 of a string and returns the hash in hex.
func sha256Hex(input string) string {
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])
}

// HMACAuth performs the full HMAC-SHA256 authentication on an OWN connection.
//
// Precondition: the connection has already received the initial ACK (*#*1##) and has sent
// *99*0## and received *98*2## as response (indicating HMAC is required).
//
// Parameters:
//   - reader: buffered reader of the TCP connection
//   - writer: function to write bytes to the connection
//   - password: device password
//   - timeout: timeout for reads
//
// Returns an error if authentication fails.
func HMACAuth(reader *bufio.Reader, writer func([]byte) error, password string, timeout time.Duration) error {
	// Step 1: Send ACK to accept HMAC challenge
	if err := writer([]byte(CMD_ACK)); err != nil {
		return fmt.Errorf("error sending ACK for HMAC: %w", err)
	}

	// Step 2: Read server nonce (Ra): *#DIGITS##
	nonceMsg, err := readOWNMessage(reader)
	if err != nil {
		return fmt.Errorf("error reading server nonce: %w", err)
	}
	nonceMsg = strings.TrimSpace(nonceMsg)

	// Extract nonce digits: strip "*#" prefix and "##" suffix
	if !strings.HasPrefix(nonceMsg, "*#") || !strings.HasSuffix(nonceMsg, "##") {
		return fmt.Errorf("unexpected nonce format: %s", nonceMsg)
	}
	nonceDigits := strings.TrimPrefix(nonceMsg, "*#")
	nonceDigits = strings.TrimSuffix(nonceDigits, "##")

	// Step 3: Compute authentication values
	ra := digitToHex(nonceDigits)
	rb := sha256Hex(fmt.Sprintf("time%d", time.Now().UnixMilli()))
	kab := sha256Hex(password)

	hmac := sha256Hex(ra + rb + HMAC_A + HMAC_B + kab)

	// Step 4: Send response: *#<Rb_digits>*<HMAC_digits>##
	rbDigits := hexToDigit(rb)
	hmacDigits := hexToDigit(hmac)

	authResponse := fmt.Sprintf("*#%s*%s##", rbDigits, hmacDigits)
	if err := writer([]byte(authResponse)); err != nil {
		return fmt.Errorf("error sending HMAC response: %w", err)
	}

	// Step 5: Read server HMAC (server-to-us validation)
	serverHMAC, err := readOWNMessage(reader)
	if err != nil {
		return fmt.Errorf("error reading server HMAC: %w", err)
	}
	serverHMAC = strings.TrimSpace(serverHMAC)

	// Step 6: Send ACK to accept the server HMAC
	if err := writer([]byte(CMD_ACK)); err != nil {
		return fmt.Errorf("error sending final ACK: %w", err)
	}

	// Step 7: Read final result: *#*1## = success, *#*0## = failure
	result, err := readOWNMessage(reader)
	if err != nil {
		return fmt.Errorf("error reading authentication result: %w", err)
	}
	result = strings.TrimSpace(result)

	if result != CMD_ACK {
		return fmt.Errorf("HMAC authentication failed: server responded %s", result)
	}

	return nil
}

// IsLocalhost determines whether the host is localhost (no HMAC auth required).
func IsLocalhost(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1" || host == "0" || host == "0.0.0.0"
}
