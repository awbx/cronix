// Package vercel is the Vercel Cron backend adapter.
//
// Unlike crontab/systemd/kubernetes/aws-scheduler, Vercel Cron does not
// expose a runtime API for managing schedules — they live declaratively
// in `vercel.json` under the `crons[]` array, and Vercel reads that file
// at deploy time. Reconciliation here therefore means: read vercel.json
// from disk, merge cronix-owned entries with whatever non-cronix entries
// the user has in there, write it back. The operator (or CI) commits
// and deploys.
//
// Per-job artifacts (one entry per (job, scheduleIndex)):
//
//	{
//	  "path": "/api/v1/scheduled/<job>",
//	  "schedule": "*/15 * * * *"
//	}
//
// Ownership: cronix-owned entries are identified by their `path` having
// the configured trigger-path prefix (default `/api/v1/scheduled/`).
// Non-cronix entries (e.g. a hand-written `/api/cleanup` cron) are
// preserved on every apply.
//
// Authentication: Vercel fires triggers itself — there is no `cronix
// trigger` shim involved — so HMAC signing is N/A for Vercel-mounted
// jobs. Apps deploying behind this backend should set `skipVerify: true`
// on their `createCron({...})` call (or build their own verifier
// matching Vercel's `Authorization: Bearer ${CRON_SECRET}` envelope).
// See docs/src/content/docs/backends/vercel.md.
package vercel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
)

const defaultTriggerPrefix = "/api/v1/scheduled/"

// Backend is the Vercel Cron backend.
type Backend struct {
	jsonPath      string
	triggerPrefix string
}

// Options for constructing a Backend.
type Options struct {
	// JsonPath is the path to vercel.json. Defaults to "./vercel.json".
	JsonPath string
	// TriggerPathPrefix identifies cronix-owned entries. Defaults to
	// "/api/v1/scheduled/" — the SDK convention. Override only when the
	// app mounts triggers at a non-standard path.
	TriggerPathPrefix string
}

// New constructs a Backend.
func New(opts Options) (*Backend, error) {
	jp := opts.JsonPath
	if jp == "" {
		jp = "vercel.json"
	}
	tp := opts.TriggerPathPrefix
	if tp == "" {
		tp = defaultTriggerPrefix
	}
	if !strings.HasSuffix(tp, "/") {
		tp += "/"
	}
	return &Backend{jsonPath: jp, triggerPrefix: tp}, nil
}

// Name returns "vercel".
func (*Backend) Name() string { return "vercel" }

// Ensure verifies vercel.json's parent directory exists. The file
// itself may be absent — Create writes a fresh minimal one.
func (b *Backend) Ensure(_ context.Context) error {
	dir := filepath.Dir(b.jsonPath)
	if dir == "" || dir == "." {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("vercel: stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("vercel: %s is not a directory", dir)
	}
	return nil
}

// List enumerates cronix-owned entries — those whose path starts with the
// configured trigger prefix. Non-cronix crons are ignored.
func (b *Backend) List(_ context.Context) ([]backends.ManagedEntry, error) {
	doc, err := b.read()
	if err != nil {
		return nil, err
	}
	var out []backends.ManagedEntry
	indexByJob := map[string]int{}
	for i := range doc.Crons {
		c := &doc.Crons[i]
		jobName, ok := b.jobNameFromPath(c.Path)
		if !ok {
			continue
		}
		idx := indexByJob[jobName]
		indexByJob[jobName] = idx + 1
		out = append(out, backends.ManagedEntry{
			App:   "", // not encoded in vercel.json — reconciler tolerates empty
			Job:   jobName,
			Hash:  hashEntry(c.Path, c.Schedule),
			Index: idx,
			Raw:   *c,
		})
	}
	return out, nil
}

// Create installs entries for every schedule of `job`.
func (b *Backend) Create(_ context.Context, _ string, job manifest.NormalizedJob) error {
	if err := validateName(job.Name); err != nil {
		return fmt.Errorf("vercel: job name: %w", err)
	}
	doc, err := b.read()
	if err != nil {
		return err
	}
	for _, sched := range job.Schedules {
		if err := validateSchedule(sched); err != nil {
			return err
		}
		doc.Crons = append(doc.Crons, cronEntry{
			Path:     b.triggerPrefix + job.Name,
			Schedule: sched,
		})
	}
	return b.write(doc)
}

// Update replaces all cronix-owned entries for (job.Name) with freshly
// rendered ones. Implemented as Delete + Create to keep semantics
// identical with the other backends.
func (b *Backend) Update(ctx context.Context, app string, job manifest.NormalizedJob) error {
	if err := b.Delete(ctx, app, job.Name); err != nil {
		return err
	}
	return b.Create(ctx, app, job)
}

// Delete removes every cronix-owned entry for (jobName), preserving
// every non-cronix entry intact.
func (b *Backend) Delete(_ context.Context, _ string, jobName string) error {
	if err := validateName(jobName); err != nil {
		return fmt.Errorf("vercel: job name: %w", err)
	}
	doc, err := b.read()
	if err != nil {
		return err
	}
	wantPath := b.triggerPrefix + jobName
	keep := doc.Crons[:0]
	for _, c := range doc.Crons {
		if c.Path == wantPath {
			continue
		}
		keep = append(keep, c)
	}
	doc.Crons = keep
	return b.write(doc)
}

// Validate checks the job fits inside Vercel's constraints — POSIX 5-
// field cron only, no `@hourly` etc., no per-job timezone (Vercel cron
// runs in UTC).
func (*Backend) Validate(job manifest.NormalizedJob) backends.ValidationResult {
	var issues []string
	for i, s := range job.Schedules {
		if err := validateSchedule(s); err != nil {
			issues = append(issues, fmt.Sprintf("schedules[%d]: %v", i, err))
		}
	}
	if job.Timezone != "" && job.Timezone != "UTC" {
		issues = append(issues, fmt.Sprintf(
			"timezone %q: Vercel Cron runs in UTC only — drop the timezone field or accept UTC",
			job.Timezone,
		))
	}
	return backends.ValidationResult{OK: len(issues) == 0, Issues: issues}
}

// History is not supported by Vercel out of the box — the operator
// surface for run records is the Vercel dashboard / `vercel logs`.
// The History method returns an empty slice with no error so
// `cronix history` reports gracefully rather than failing.
func (*Backend) History(_ context.Context, _ backends.HistoryOpts) ([]backends.HistoryEntry, error) {
	return nil, nil
}

// ───── internals ────────────────────────────────────────────────────

// vercelJSON mirrors the subset of vercel.json we touch. Unknown fields
// are preserved verbatim via the `Other` rawMessage map so writes don't
// strip anything the user has configured.
type vercelJSON struct {
	Crons []cronEntry                `json:"crons,omitempty"`
	Other map[string]json.RawMessage `json:"-"`
}

type cronEntry struct {
	Path     string `json:"path"`
	Schedule string `json:"schedule"`
}

// MarshalJSON writes the standard fields plus any preserved unknowns.
func (d vercelJSON) MarshalJSON() ([]byte, error) {
	out := map[string]json.RawMessage{}
	maps.Copy(out, d.Other)
	if len(d.Crons) > 0 {
		// Sort by (path, schedule) so writes are deterministic — same
		// input always produces byte-identical vercel.json. Helps the
		// idempotency contract (D-027).
		c := append([]cronEntry(nil), d.Crons...)
		sort.SliceStable(c, func(i, j int) bool {
			if c[i].Path != c[j].Path {
				return c[i].Path < c[j].Path
			}
			return c[i].Schedule < c[j].Schedule
		})
		raw, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		out["crons"] = raw
	} else {
		// Empty crons → drop the key entirely so apps that never had
		// one don't grow one after the first apply.
		delete(out, "crons")
	}
	// Re-emit deterministically (alphabetical key order).
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf strings.Builder
	buf.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			buf.WriteString(",")
		}
		raw, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(raw)
		buf.WriteString(":")
		buf.Write(out[k])
	}
	buf.WriteString("}")
	return []byte(buf.String()), nil
}

func (d *vercelJSON) UnmarshalJSON(b []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if rawCrons, ok := raw["crons"]; ok {
		if err := json.Unmarshal(rawCrons, &d.Crons); err != nil {
			return fmt.Errorf("vercel: parse crons: %w", err)
		}
		delete(raw, "crons")
	}
	d.Other = raw
	return nil
}

func (b *Backend) read() (*vercelJSON, error) {
	data, err := os.ReadFile(b.jsonPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &vercelJSON{Other: map[string]json.RawMessage{}}, nil
		}
		return nil, fmt.Errorf("vercel: read %s: %w", b.jsonPath, err)
	}
	if len(data) == 0 {
		return &vercelJSON{Other: map[string]json.RawMessage{}}, nil
	}
	var doc vercelJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("vercel: parse %s: %w", b.jsonPath, err)
	}
	if doc.Other == nil {
		doc.Other = map[string]json.RawMessage{}
	}
	return &doc, nil
}

// write serializes the doc with two-space indentation matching Vercel's
// own convention, plus a trailing newline so editors don't bicker.
func (b *Backend) write(doc *vercelJSON) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("vercel: marshal: %w", err)
	}
	var pretty []byte
	pretty, err = jsonIndent(raw, 2)
	if err != nil {
		return fmt.Errorf("vercel: indent: %w", err)
	}
	pretty = append(pretty, '\n')
	if err := os.WriteFile(b.jsonPath, pretty, 0o644); err != nil {
		return fmt.Errorf("vercel: write %s: %w", b.jsonPath, err)
	}
	return nil
}

func jsonIndent(raw []byte, spaces int) ([]byte, error) {
	var buf bytes.Buffer
	indent := strings.Repeat(" ", spaces)
	if err := json.Indent(&buf, raw, "", indent); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (b *Backend) jobNameFromPath(p string) (string, bool) {
	if !strings.HasPrefix(p, b.triggerPrefix) {
		return "", false
	}
	rest := p[len(b.triggerPrefix):]
	if rest == "" || strings.ContainsAny(rest, "/?#") {
		return "", false
	}
	return rest, true
}

// validateSchedule enforces Vercel's POSIX 5-field cron requirement.
// `@hourly` / `@daily` / `@every Ns` are rejected — the user must either
// translate them to 5-field at the manifest layer or pick a different
// backend.
func validateSchedule(s string) error {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "@") {
		return fmt.Errorf("vercel: schedule %q — Vercel Cron requires 5-field POSIX cron (no @hourly / @daily etc.)", s)
	}
	if len(strings.Fields(t)) != 5 {
		return fmt.Errorf("vercel: schedule %q is not 5-field cron", s)
	}
	return nil
}

func validateName(s string) error {
	if s == "" {
		return errors.New("name is empty")
	}
	for i, r := range s {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		isHyphen := r == '-'
		if !isLower && !isDigit && !isHyphen {
			return fmt.Errorf("name %q has invalid char at %d", s, i)
		}
	}
	return nil
}

// hashEntry is a stable identity for List's drift comparison. Vercel
// lacks per-entry metadata so we synthesize one from the wire-visible
// fields (path + schedule). reconcile.Compute compares this against
// `policy.Hash(job, idx)` to detect drift.
func hashEntry(path, schedule string) string {
	return fmt.Sprintf("vercel:%s:%s", path, schedule)
}

var _ backends.Backend = (*Backend)(nil)
