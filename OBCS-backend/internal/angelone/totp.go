// Package angelone is a minimal AngelOne SmartAPI client covering login,
// market data and order placement. It is only exercised in live mode; paper
// mode never constructs a live client. Credentials are passed in from config
// and never logged.
package angelone

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// totpNow computes the current 6-digit TOTP for a base32 secret (RFC 6238,
// 30-second period, SHA1). Ported from simulator._totp_now.
func totpNow(secret string) (string, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	if pad := len(s) % 8; pad != 0 {
		s += strings.Repeat("=", 8-pad)
	}
	key, err := base32.StdEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("decode totp secret: %w", err)
	}
	counter := uint64(time.Now().Unix()) / 30
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	h := mac.Sum(nil)

	off := h[len(h)-1] & 0x0F
	code := (binary.BigEndian.Uint32(h[off:off+4]) & 0x7FFFFFFF) % 1_000_000
	return fmt.Sprintf("%06d", code), nil
}
