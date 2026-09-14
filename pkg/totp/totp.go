// Package totp implements time-based one-time passwords (RFC 6238) over
// HMAC-SHA1 (RFC 4226), plus the base32 secret handling and otpauth:// URI
// that authenticator apps expect.
//
// It is implemented here rather than pulled in as a dependency because the
// whole algorithm is about eighty lines of standard library, and an auth
// primitive is worth being able to read end to end.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Period is the code lifetime in seconds. Thirty is what every
	// authenticator app assumes.
	Period = 30
	// Digits is the code length.
	Digits = 6
	// DefaultSkew is how many periods either side of now are accepted. One
	// period each way tolerates a phone clock that is up to thirty seconds
	// out, which is common, without meaningfully widening the window.
	DefaultSkew = 1
	// secretBytes is the raw entropy in a generated secret. RFC 4226 requires
	// at least 128 bits and recommends 160, which is what this is.
	secretBytes = 20
)

// b32 is unpadded standard base32: what authenticator apps accept in a
// manually typed secret.
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret returns a new base32-encoded shared secret.
func GenerateSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("totp: generate secret: %w", err)
	}
	return b32.EncodeToString(buf), nil
}

// Code returns the code for a secret at a point in time.
func Code(secret string, t time.Time) (string, error) {
	key, err := decode(secret)
	if err != nil {
		return "", err
	}
	return hotp(key, uint64(t.Unix()/Period)), nil
}

// Validate reports whether code is correct for secret at time t, allowing the
// given number of periods of clock skew either side.
//
// Comparison is constant-time. A timing side channel on a six-digit code is
// not a realistic attack on its own, but the cost of avoiding it is one
// function call.
func Validate(secret, code string, t time.Time, skew int) (bool, error) {
	key, err := decode(secret)
	if err != nil {
		return false, err
	}
	code = normalize(code)
	if len(code) != Digits {
		return false, nil
	}
	if skew < 0 {
		skew = 0
	}

	counter := t.Unix() / Period
	ok := false
	for i := -skew; i <= skew; i++ {
		candidate := hotp(key, uint64(counter+int64(i)))
		// No early exit: every candidate is compared so the loop takes the
		// same time whichever one matches.
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			ok = true
		}
	}
	return ok, nil
}

// URI builds the otpauth:// URI an authenticator app scans from a QR code.
// issuer is the application name; account identifies the user within it.
func URI(secret, issuer, account string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(Digits))
	q.Set("period", fmt.Sprint(Period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// FormatSecret groups a secret into blocks of four for manual entry, which is
// what someone does when they cannot scan the QR code.
func FormatSecret(secret string) string {
	var b strings.Builder
	for i, r := range secret {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// hotp is the RFC 4226 HMAC-based one-time password.
func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	// Dynamic truncation: the low nibble of the last byte picks the offset.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for i := 0; i < Digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", Digits, value%mod)
}

// decode parses a base32 secret, tolerating the spacing and lowercase a user
// may have typed.
func decode(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(secret))
	s = strings.TrimRight(s, "=")
	if s == "" {
		return nil, fmt.Errorf("totp: empty secret")
	}
	key, err := b32.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("totp: decode secret: %w", err)
	}
	return key, nil
}

// normalize strips the spacing an authenticator app displays in a code.
func normalize(code string) string {
	return strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code))
}
