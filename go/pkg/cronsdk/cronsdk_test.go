package cronsdk

import (
	"bytes"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/awbx/cronix/go/internal/auth"
)

const testSecret = "whsec_test_primary_aaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestVerifyOK(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	signed, err := auth.Sign(auth.SignOptions{
		Secret: testSecret, Method: "POST", Path: "/api/v1/scheduled/x",
		Body: body, Timestamp: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	res, err := Verify(VerifyOptions{
		Secrets: []string{testSecret},
		Method:  "POST", Path: "/api/v1/scheduled/x",
		Body: body, Header: signed.Header,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.SecretIndex != 0 {
		t.Errorf("secretIndex = %d, want 0", res.SecretIndex)
	}
}

func TestVerifyMismatch(t *testing.T) {
	body := []byte("hi")
	signed, _ := auth.Sign(auth.SignOptions{
		Secret: testSecret, Method: "POST", Path: "/x", Body: body, Timestamp: time.Now().Unix(),
	})
	_, err := Verify(VerifyOptions{
		Secrets: []string{"different-secret-zzzzzzzzzzzzzzzzzzzzzzzzzz"},
		Method:  "POST", Path: "/x", Body: body, Header: signed.Header,
	})
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("expected ErrSignatureMismatch, got %v", err)
	}
}

func TestVerifyMalformedHeader(t *testing.T) {
	_, err := Verify(VerifyOptions{
		Secrets: []string{testSecret},
		Method:  "POST", Path: "/x", Body: nil, Header: "garbage",
	})
	if !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("expected ErrMalformedHeader, got %v", err)
	}
}

func TestVerifyStale(t *testing.T) {
	body := []byte("x")
	signed, _ := auth.Sign(auth.SignOptions{
		Secret: testSecret, Method: "POST", Path: "/x", Body: body, Timestamp: 1700000000,
	})
	_, err := Verify(VerifyOptions{
		Secrets: []string{testSecret},
		Method:  "POST", Path: "/x", Body: body, Header: signed.Header,
		Now: 1700000000 + 1000, MaxSkewSeconds: 60,
	})
	if !errors.Is(err, ErrStaleTimestamp) {
		t.Fatalf("expected ErrStaleTimestamp, got %v", err)
	}
}

func TestVerifyHTTPHelper(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	signed, _ := auth.Sign(auth.SignOptions{
		Secret: testSecret, Method: "POST", Path: "/api/v1/scheduled/x",
		Body: body, Timestamp: time.Now().Unix(),
	})
	req, err := http.NewRequest("POST", "https://example.com/api/v1/scheduled/x", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set(HeaderSignature, signed.Header)
	res, err := VerifyHTTP(req, body, []string{testSecret})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.SecretIndex != 0 {
		t.Errorf("secretIndex = %d", res.SecretIndex)
	}
}

func TestRotation(t *testing.T) {
	const old = testSecret
	const newer = "whsec_test_rotated_bbbbbbbbbbbbbbbbbbbbbbbbbbb"
	body := []byte("rotate")
	signed, _ := auth.Sign(auth.SignOptions{
		Secret: old, Method: "POST", Path: "/x", Body: body, Timestamp: time.Now().Unix(),
	})
	res, err := Verify(VerifyOptions{
		Secrets: []string{newer, old},
		Method:  "POST", Path: "/x", Body: body, Header: signed.Header,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.SecretIndex != 1 {
		t.Errorf("expected old (index 1) to match, got %d", res.SecretIndex)
	}
}

func TestHeaderConstants(t *testing.T) {
	if HeaderSignature != "X-Cron-Signature" {
		t.Errorf("Signature header drift: %q", HeaderSignature)
	}
	if HeaderRunID != "X-Cron-Run-Id" {
		t.Errorf("RunID header drift: %q", HeaderRunID)
	}
}
