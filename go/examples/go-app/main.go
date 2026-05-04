// Package main is a runnable example of an app verifying cronix triggers
// using the public Go SDK at github.com/awbx/cronix/go/pkg/cronsdk.
//
// Run:
//
//	cd go/examples/go-app
//	CRON_SECRET=whsec_dev go run .
//
// Then post a signed trigger from a separate shell:
//
//	cronix trigger billing.reconcile-payments --spec-dir ./specs
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/awbx/cronix/go/pkg/cronsdk"
)

func secrets() []string {
	out := []string{}
	for _, k := range []string{"CRON_SECRET_V2", "CRON_SECRET", "CRON_SECRET_V1"} {
		if v := os.Getenv(k); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		out = append(out, "whsec_dev_primary")
	}
	return out
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/scheduled/", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		res, err := cronsdk.VerifyHTTP(r, body, secrets())
		if err != nil {
			status := http.StatusUnauthorized
			switch {
			case errors.Is(err, cronsdk.ErrMalformedHeader):
				status = http.StatusUnauthorized
			case errors.Is(err, cronsdk.ErrStaleTimestamp):
				status = http.StatusUnauthorized
			case errors.Is(err, cronsdk.ErrSignatureMismatch):
				status = http.StatusUnauthorized
			}
			http.Error(w, err.Error(), status)
			return
		}

		runID := r.Header.Get(cronsdk.HeaderRunID)
		schedule := r.Header.Get(cronsdk.HeaderScheduleName)
		attempt := r.Header.Get(cronsdk.HeaderAttempt)
		log.Printf("cron fire: schedule=%s run=%s attempt=%s secret_index=%d body_len=%d",
			schedule, runID, attempt, res.SecretIndex, len(body))

		// App-side dedup hook would go here, keyed on runID.

		fmt.Fprintf(w, "ok runID=%s\n", runID)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	addr := ":" + port
	log.Printf("go-app listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
