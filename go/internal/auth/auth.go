// Package auth implements HMAC-SHA256 signing and verification for cronix
// manifests and triggers. Stripe-shaped header `t=<unix>,v1=<hex>` (D-014..
// D-019). Verification is constant-time. The conformance suite at
// spec/auth-vectors.json defines correctness; both this package and the
// @cronix/sdk TypeScript implementation must pass every vector.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	SigVersion                 = "v1"
	ReplayWindowDefaultSeconds = 300
	expectedSignatureHexLen    = 64
)

// Sentinel errors. Use errors.Is() to discriminate.
var (
	ErrMalformedHeader   = errors.New("auth: malformed signature header")
	ErrStaleTimestamp    = errors.New("auth: timestamp outside replay window")
	ErrSignatureMismatch = errors.New("auth: no acceptable secret produced a matching signature")
)

// SignOptions describes a request to sign.
//
// Body may be nil; it is treated as empty bytes. Method and Path are part of
// the signed payload (D-015) so that a valid signature for one endpoint
// cannot be replayed against another.
type SignOptions struct {
	Secret    string
	Method    string
	Path      string
	Body      []byte
	Timestamp int64 // unix seconds; 0 means "now" via the package-level Clock
}

// SignResult holds a constructed signature header and its embedded timestamp.
type SignResult struct {
	Header    string
	Timestamp int64
}

// VerifyOptions describes a request whose signature is being checked.
type VerifyOptions struct {
	Secrets        []string
	Method         string
	Path           string
	Body           []byte
	Header         string
	Now            int64 // unix seconds; 0 means "now" via the package-level Clock
	MaxSkewSeconds int   // 0 means use ReplayWindowDefaultSeconds
}

// VerifyResult identifies which secret matched.
type VerifyResult struct {
	SecretIndex int
}

// Clock is the package-level clock. Tests inject their own.
var Clock = func() time.Time { return time.Now() }

func nowUnix() int64 { return Clock().Unix() }

// Sign computes the signature header for the given options.
func Sign(o SignOptions) (SignResult, error) {
	if len(o.Secret) == 0 {
		return SignResult{}, errors.New("auth: secret must be non-empty")
	}
	ts := o.Timestamp
	if ts == 0 {
		ts = nowUnix()
	}
	payload := canonicalSignedPayload(ts, o.Method, o.Path, o.Body)
	mac := hmacHex(o.Secret, payload)
	return SignResult{
		Header:    fmt.Sprintf("t=%d,%s=%s", ts, SigVersion, mac),
		Timestamp: ts,
	}, nil
}

// Verify checks the signature header against any of the supplied secrets.
// Returns the index of the secret that matched, or one of the sentinel
// errors wrapped with explanatory context.
func Verify(o VerifyOptions) (VerifyResult, error) {
	if len(o.Secrets) == 0 {
		return VerifyResult{}, fmt.Errorf("%w: at least one secret required", ErrMalformedHeader)
	}
	ts, sigBytes, err := parseHeader(o.Header)
	if err != nil {
		return VerifyResult{}, err
	}
	now := o.Now
	if now == 0 {
		now = nowUnix()
	}
	skew := o.MaxSkewSeconds
	if skew == 0 {
		skew = ReplayWindowDefaultSeconds
	}
	delta := now - ts
	if delta < 0 {
		delta = -delta
	}
	if delta > int64(skew) {
		return VerifyResult{}, fmt.Errorf("%w: ts=%d now=%d skew=%d", ErrStaleTimestamp, ts, now, skew)
	}
	payload := canonicalSignedPayload(ts, o.Method, o.Path, o.Body)
	for i, secret := range o.Secrets {
		if len(secret) == 0 {
			continue
		}
		expected := hmacBytes(secret, payload)
		if subtle.ConstantTimeCompare(expected, sigBytes) == 1 {
			return VerifyResult{SecretIndex: i}, nil
		}
	}
	return VerifyResult{}, ErrSignatureMismatch
}

// canonicalSignedPayload builds `<unix>.<METHOD>.<path>.<body>` (D-015).
func canonicalSignedPayload(ts int64, method, path string, body []byte) []byte {
	prefix := fmt.Sprintf("%d.%s.%s.", ts, strings.ToUpper(method), path)
	out := make([]byte, 0, len(prefix)+len(body))
	out = append(out, prefix...)
	out = append(out, body...)
	return out
}

func hmacBytes(secret string, payload []byte) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return h.Sum(nil)
}

func hmacHex(secret string, payload []byte) string {
	return hex.EncodeToString(hmacBytes(secret, payload))
}

func parseHeader(header string) (int64, []byte, error) {
	if header == "" {
		return 0, nil, fmt.Errorf("%w: header is empty", ErrMalformedHeader)
	}
	var (
		ts     int64
		sigHex string
		hasTS  bool
		hasSig bool
	)
	for _, part := range strings.Split(header, ",") {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return 0, nil, fmt.Errorf("%w: malformed segment %q", ErrMalformedHeader, part)
		}
		k := part[:eq]
		v := part[eq+1:]
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return 0, nil, fmt.Errorf("%w: t must be a non-negative integer: %q", ErrMalformedHeader, v)
			}
			ts = n
			hasTS = true
		case SigVersion:
			if len(v) != expectedSignatureHexLen {
				return 0, nil, fmt.Errorf("%w: %s must be %d hex chars", ErrMalformedHeader, SigVersion, expectedSignatureHexLen)
			}
			sigHex = strings.ToLower(v)
			hasSig = true
		default:
			// forward-compatible: unknown segments ignored
		}
	}
	if !hasTS {
		return 0, nil, fmt.Errorf("%w: missing `t=`", ErrMalformedHeader)
	}
	if !hasSig {
		return 0, nil, fmt.Errorf("%w: missing `%s=`", ErrMalformedHeader, SigVersion)
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %s is not valid hex", ErrMalformedHeader, SigVersion)
	}
	return ts, sig, nil
}
