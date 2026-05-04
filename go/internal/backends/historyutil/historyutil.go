// Package historyutil parses the trigger shim's slog-JSON output into
// HistoryEntry records, regardless of whether the lines arrive wrapped
// in journald JSON (systemd backend) or as raw stdout (k8s pod logs).
//
// The shim emits one slog record per attempt. We fold them into one
// HistoryEntry per terminal run, keyed on `run_id`.
package historyutil

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/awbx/cronix/go/internal/backends"
)

// ShimEvent is the subset of slog-emitted fields the shim writes per attempt.
// App / Job land via the WithGroup setup in shim.go and are present on
// every record from a real cronix trigger.
type ShimEvent struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Msg     string `json:"msg"`
	App     string `json:"app"`
	Job     string `json:"job"`
	RunID   string `json:"run_id"`
	Status  int    `json:"status"`
	Attempt int    `json:"attempt"`
}

// JournalRecord is the subset of fields we read from `journalctl --output=json`.
type JournalRecord struct {
	Timestamp string `json:"__REALTIME_TIMESTAMP"`
	Unit      string `json:"_SYSTEMD_UNIT"`
	Message   string `json:"MESSAGE"`
}

// FoldShimLogs takes raw bytes containing one record per line, where
// each record is either:
//
//  1. journald-wrapped JSON with a MESSAGE field that itself is shim
//     slog JSON (systemd / crontab paths); or
//  2. raw shim slog JSON (k8s pod logs, plain stdout).
//
// Returns one HistoryEntry per run_id, keeping the *terminal* status
// when both pre-terminal and terminal events are present for the same run.
//
// app, job, source are stamped onto every emitted entry. When the
// shim event itself carries non-empty `app` / `job` fields and they
// don't match the supplied app/job, the record is skipped — useful
// for the crontab path where one journalctl query returns runs from
// every cronix-managed job on the host. status filter is applied
// last; pass "" to keep all.
func FoldShimLogs(raw []byte, app, job, source, statusFilter string) []backends.HistoryEntry {
	type byRun struct {
		entry backends.HistoryEntry
		seen  bool
	}
	runs := map[string]*byRun{}
	order := make([]string, 0)
	for line := range strings.SplitSeq(strings.TrimRight(string(raw), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ev, when, ok := decodeLine([]byte(line))
		if !ok || ev.RunID == "" {
			continue
		}
		if app != "" && ev.App != "" && ev.App != app {
			continue
		}
		if job != "" && ev.Job != "" && ev.Job != job {
			continue
		}
		status, terminal := ClassifyShimEvent(ev.Msg)
		if status == "" {
			continue
		}
		entry, ok := runs[ev.RunID]
		if !ok {
			entry = &byRun{}
			runs[ev.RunID] = entry
			order = append(order, ev.RunID)
		}
		he := &entry.entry
		if !entry.seen || terminal {
			he.RunID = ev.RunID
			he.App = app
			he.Job = job
			he.Attempt = ev.Attempt
			he.Status = status
			he.Source = source
			he.FinishedAt = when
			if he.StartedAt.IsZero() || when.Before(he.StartedAt) {
				he.StartedAt = when
			}
			entry.seen = true
		}
	}
	out := make([]backends.HistoryEntry, 0, len(order))
	for _, id := range order {
		he := runs[id].entry
		if statusFilter != "" && he.Status != statusFilter {
			continue
		}
		out = append(out, he)
	}
	return out
}

// decodeLine handles both shapes: journald-wrapped first, then raw.
func decodeLine(line []byte) (ShimEvent, time.Time, bool) {
	var rec JournalRecord
	if err := json.Unmarshal(line, &rec); err == nil && rec.Message != "" {
		var ev ShimEvent
		if err := json.Unmarshal([]byte(rec.Message), &ev); err == nil && ev.RunID != "" {
			return ev, parseJournalTimestamp(rec.Timestamp), true
		}
	}
	var ev ShimEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return ShimEvent{}, time.Time{}, false
	}
	when, _ := time.Parse(time.RFC3339, ev.Time)
	return ev, when.UTC(), true
}

// ClassifyShimEvent maps the shim's slog `msg` tag to a HistoryEntry
// status. Returns ("", false) when the message isn't a fire-lifecycle
// event (early errors, lock acquire diagnostics, etc.).
func ClassifyShimEvent(msg string) (status string, terminal bool) {
	switch msg {
	case "trigger: success":
		return "ok", true
	case "trigger: app rejected":
		return "failed", true
	case "trigger: retries exhausted":
		return "failed", true
	case "trigger: lock contended":
		return "lock-contended", true
	case "trigger: panic":
		return "failed", true
	case "trigger: attempt failed", "trigger: server error":
		return "failed", false
	}
	return "", false
}

// parseJournalTimestamp converts journalctl's `__REALTIME_TIMESTAMP`
// (microseconds since epoch as a decimal string) into time.Time.
func parseJournalTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	micros, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMicro(micros).UTC()
}
