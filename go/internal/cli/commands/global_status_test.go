package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/awbx/cronix/go/internal/cli/config"
)

// TestGlobalStatus_endToEnd builds a Config with two crontab entries
// pointing at temp files, runs the full command, and verifies output.
func TestGlobalStatus_endToEnd(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "crontab.one")
	two := filepath.Join(dir, "crontab.two")

	mustWrite(t, one, `*/15 * * * * /bin/cronix trigger billing.reconcile-payments
# cronix:owned app=billing job=reconcile-payments hash=abc1234567890def idx=0
0 0 * * * /bin/cronix trigger billing.nightly-rollup
# cronix:owned app=billing job=nightly-rollup hash=fedcba9876543210 idx=0
`)
	mustWrite(t, two, `0 * * * * /bin/cronix trigger analytics.send-report
# cronix:owned app=analytics job=send-report hash=1111222233334444 idx=0
`)

	cfg := &config.Config{
		Version: 1,
		Backends: []config.BackendEntry{
			{Name: "host-a", Type: "crontab", CrontabPath: one, TriggerBin: "/bin/cronix"},
			{Name: "host-b", Type: "crontab", CrontabPath: two, TriggerBin: "/bin/cronix"},
		},
	}

	results := runGlobalStatus(context.Background(), cfg, globalStatusOpts{
		parallel:          4,
		perBackendTimeout: 5 * time.Second,
	})

	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	// Stable order matches config order.
	if results[0].Entry.Name != "host-a" || results[1].Entry.Name != "host-b" {
		t.Errorf("result order = [%s, %s], want [host-a, host-b]",
			results[0].Entry.Name, results[1].Entry.Name)
	}
	if got, want := len(results[0].Items), 2; got != want {
		t.Errorf("host-a items = %d, want %d", got, want)
	}
	if got, want := len(results[1].Items), 1; got != want {
		t.Errorf("host-b items = %d, want %d", got, want)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Entry.Name, r.Err)
		}
	}
}

func TestGlobalStatus_filterByName(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	mustWrite(t, a, "")
	mustWrite(t, b, "")

	cfg := &config.Config{
		Version: 1,
		Backends: []config.BackendEntry{
			{Name: "alpha", Type: "crontab", CrontabPath: a, TriggerBin: "/bin/cronix"},
			{Name: "beta", Type: "crontab", CrontabPath: b, TriggerBin: "/bin/cronix"},
		},
	}

	results := runGlobalStatus(context.Background(), cfg, globalStatusOpts{
		filterNames:       []string{"beta"},
		parallel:          2,
		perBackendTimeout: 5 * time.Second,
	})
	if len(results) != 1 || results[0].Entry.Name != "beta" {
		t.Fatalf("filter failed; got %d results, names=%v", len(results), names(results))
	}
}

func TestGlobalStatus_brokenBackendIsIsolated(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	mustWrite(t, good, `*/5 * * * * /bin/cronix trigger app.job
# cronix:owned app=app job=job hash=deadbeefcafebabe idx=0
`)

	cfg := &config.Config{
		Version: 1,
		Backends: []config.BackendEntry{
			// Missing TriggerBin → crontab.New() returns an error; the
			// good entry must still produce results.
			{Name: "broken", Type: "crontab", CrontabPath: good},
			{Name: "good", Type: "crontab", CrontabPath: good, TriggerBin: "/bin/cronix"},
		},
	}

	results := runGlobalStatus(context.Background(), cfg, globalStatusOpts{
		parallel:          2,
		perBackendTimeout: 5 * time.Second,
	})

	var gotErr, gotOK bool
	for _, r := range results {
		switch r.Entry.Name {
		case "broken":
			if r.Err == nil {
				t.Errorf("broken entry: want error, got nil")
			}
			gotErr = true
		case "good":
			if r.Err != nil {
				t.Errorf("good entry: unexpected error: %v", r.Err)
			}
			if len(r.Items) != 1 {
				t.Errorf("good entry: want 1 item, got %d", len(r.Items))
			}
			gotOK = true
		}
	}
	if !gotErr || !gotOK {
		t.Fatalf("expected one ERROR + one OK; gotErr=%v gotOK=%v", gotErr, gotOK)
	}
}

func TestGlobalStatus_tableOutput(t *testing.T) {
	cmd := newGlobalStatusCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	dir := t.TempDir()
	one := filepath.Join(dir, "crontab")
	mustWrite(t, one, `*/15 * * * * /bin/cronix trigger billing.reconcile-payments
# cronix:owned app=billing job=reconcile-payments hash=abc1234567890def idx=0
`)
	cfgPath := filepath.Join(dir, "config.yaml")
	mustWrite(t, cfgPath, "version: 1\nbackends:\n  - name: host-a\n    type: crontab\n    crontab_path: "+one+"\n    trigger_bin: /bin/cronix\n")

	cmd.SetArgs([]string{"--config", cfgPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"BACKEND", "host-a", "billing", "reconcile-payments", "OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestGlobalStatus_jsonOutput(t *testing.T) {
	cmd := newGlobalStatusCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	dir := t.TempDir()
	one := filepath.Join(dir, "crontab")
	mustWrite(t, one, `*/15 * * * * /bin/cronix trigger billing.reconcile-payments
# cronix:owned app=billing job=reconcile-payments hash=abc1234567890def idx=0
`)
	cfgPath := filepath.Join(dir, "config.yaml")
	mustWrite(t, cfgPath, "version: 1\nbackends:\n  - name: host-a\n    type: crontab\n    crontab_path: "+one+"\n    trigger_bin: /bin/cronix\n")

	cmd.SetArgs([]string{"--config", cfgPath, "-o", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rep globalStatusReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("decode json: %v\n--- output ---\n%s", err, buf.String())
	}
	if len(rep.Backends) != 1 || rep.Backends[0].Name != "host-a" {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if len(rep.Backends[0].Entries) != 1 || rep.Backends[0].Entries[0].Job != "reconcile-payments" {
		t.Errorf("unexpected entries: %+v", rep.Backends[0].Entries)
	}
}

func TestGlobalStatus_strictExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	mustWrite(t, cfgPath, "version: 1\nbackends:\n  - name: broken\n    type: crontab\n    crontab_path: /tmp/whatever\n  - name: ok\n    type: crontab\n    crontab_path: /tmp/whatever\n    trigger_bin: /bin/cronix\n")

	cmd := newGlobalStatusCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--config", cfgPath, "--strict"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-nil error from --strict, got nil")
	}
	if !strings.Contains(err.Error(), "errored") {
		t.Errorf("error = %q, want to mention 'errored'", err.Error())
	}
}

func TestGlobalStatus_noConfigFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := newGlobalStatusCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	// No --config; default lookup should find nothing under fake HOME
	// (and /etc/cronix/config.yaml is not present in test env).
	if _, err := os.Stat("/etc/cronix/config.yaml"); err == nil {
		t.Skip("/etc/cronix/config.yaml exists on this host; skipping no-config test")
	}
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no config found")
	}
	if !strings.Contains(err.Error(), "no config found") {
		t.Errorf("error = %q, want to mention 'no config found'", err.Error())
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(rs []queryResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Entry.Name
	}
	return out
}
