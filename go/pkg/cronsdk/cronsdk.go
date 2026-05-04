// Package cronsdk is the public Go SDK for cronix.
//
// Scope (v1): HMAC-SHA256 trigger signature verification, plus the
// X-Cron-* HTTP header constants apps need to read run-id, attempt,
// fire times etc. from incoming triggers.
//
// Full Go SDK with manifest registration and dispatch is deferred to
// v2 — the TypeScript reference SDK (@awbx/cronix-sdk) is the v1 target for
// app-side declaration. Apps in Go that need to register schedules
// today should write the manifest JSON directly and use this package
// only for the verification side.
//
// # Quickstart
//
//	import (
//	    "log"
//	    "net/http"
//
//	    "github.com/awbx/cronix/go/pkg/cronsdk"
//	)
//
//	func main() {
//	    secrets := []string{
//	        os.Getenv("CRON_SECRET_V2"),
//	        os.Getenv("CRON_SECRET_V1"),
//	    }
//
//	    http.HandleFunc("/api/v1/scheduled/reconcile-payments", func(w http.ResponseWriter, r *http.Request) {
//	        body, _ := io.ReadAll(r.Body)
//	        if _, err := cronsdk.VerifyHTTP(r, body, secrets); err != nil {
//	            http.Error(w, err.Error(), http.StatusUnauthorized)
//	            return
//	        }
//	        runID := r.Header.Get(cronsdk.HeaderRunID)
//	        log.Printf("running reconcile-payments runID=%s", runID)
//	        w.WriteHeader(http.StatusAccepted)
//	    })
//	    log.Fatal(http.ListenAndServe(":3000", nil))
//	}
//
// # Conformance
//
// This package re-exports cronix's reference HMAC verifier from
// internal/auth. Both this package and @awbx/cronix-sdk pass every case in
// spec/auth-vectors.json byte-for-byte.
package cronsdk

import (
	"errors"
	"net/http"

	"github.com/awbx/cronix/go/internal/auth"
	"github.com/awbx/cronix/go/internal/headers"
)

// Header name constants. Mirror the values in @awbx/cronix-sdk's headers.ts.
const (
	HeaderSignature           = headers.Signature
	HeaderRunID               = headers.RunID
	HeaderScheduleName        = headers.ScheduleName
	HeaderFireTime            = headers.FireTime
	HeaderFireTimeActual      = headers.FireTimeActual
	HeaderAttempt             = headers.Attempt
	HeaderPreviousSuccessTime = headers.PreviousSuccessTime
)

// Sentinel errors. Use errors.Is to discriminate.
var (
	ErrMalformedHeader   = auth.ErrMalformedHeader
	ErrStaleTimestamp    = auth.ErrStaleTimestamp
	ErrSignatureMismatch = auth.ErrSignatureMismatch
)

// VerifyOptions are the inputs to a manual Verify call.
type VerifyOptions struct {
	Secrets        []string
	Method         string
	Path           string
	Body           []byte
	Header         string
	Now            int64 // unix seconds; 0 = time.Now()
	MaxSkewSeconds int   // 0 = 300
}

// Result identifies which secret matched.
type Result struct {
	SecretIndex int
}

// Verify checks an HMAC-SHA256 signature header against the supplied
// secrets. Returns the index of the matching secret on success, or one
// of ErrMalformedHeader / ErrStaleTimestamp / ErrSignatureMismatch on
// failure (use errors.Is to discriminate).
//
// Comparison is constant-time.
func Verify(o VerifyOptions) (Result, error) {
	res, err := auth.Verify(auth.VerifyOptions{
		Secrets:        o.Secrets,
		Method:         o.Method,
		Path:           o.Path,
		Body:           o.Body,
		Header:         o.Header,
		Now:            o.Now,
		MaxSkewSeconds: o.MaxSkewSeconds,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{SecretIndex: res.SecretIndex}, nil
}

// VerifyHTTP is the convenience wrapper for an http.Request. It pulls
// the signature out of the X-Cron-Signature header, builds the request
// path-and-query string, and delegates to Verify.
//
// The caller is responsible for supplying the verbatim body bytes —
// http.Request.Body is read-once, so apps must read it (or use a
// raw-body middleware) before calling VerifyHTTP.
func VerifyHTTP(r *http.Request, body []byte, secrets []string) (Result, error) {
	if r == nil {
		return Result{}, errors.New("cronsdk: nil request")
	}
	header := r.Header.Get(HeaderSignature)
	return Verify(VerifyOptions{
		Secrets: secrets,
		Method:  r.Method,
		Path:    r.URL.RequestURI(),
		Body:    body,
		Header:  header,
	})
}
