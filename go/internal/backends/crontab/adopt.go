package crontab

import (
	"context"
	"fmt"
	"strings"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/policy"
)

// Adopt finds existing crontab lines that already invoke
// `<triggerBin> trigger <app>.<jobName>` and applies the
// cronix:owned marker so they come under management without
// re-creation. Implements backends.Adopter.
//
// Strict matching: a line is a candidate only if its command tail
// is exactly `<triggerBin> trigger <app>.<jobName>`. Lines invoking
// the trigger with extra arguments, a wrapper script, or a
// different triggerBin path are not adopted — they show up as
// divergences for the operator to reconcile manually.
//
// Multi-schedule jobs: every manifest schedule must have a
// matching candidate line. A partial match (some schedules
// without candidates) is reported as Diverged with explanations,
// not partially adopted — partial adoption would leave the
// crontab in a confusing state where some schedules are managed
// and others fall back to Create on the next apply.
func (b *Backend) Adopt(_ context.Context, app string, job manifest.NormalizedJob, opts backends.AdoptOpts) (backends.AdoptResult, error) {
	if !appRe.MatchString(app) {
		return backends.AdoptResult{}, fmt.Errorf("crontab: invalid app id %q", app)
	}
	if !jobRe.MatchString(job.Name) {
		return backends.AdoptResult{}, fmt.Errorf("crontab: invalid job name %q", job.Name)
	}

	_, lines, err := b.readFile()
	if err != nil {
		return backends.AdoptResult{}, err
	}

	expectedTail := fmt.Sprintf("%s trigger %s.%s", b.triggerBin, app, job.Name)

	// Translate every manifest schedule once, fail-fast on anything the
	// crontab backend cannot express.
	type wantedSchedule struct {
		idx       int
		manifest  string
		fiveField string
	}
	wanted := make([]wantedSchedule, 0, len(job.Schedules))
	for i, s := range job.Schedules {
		ff, ok := translate(s)
		if !ok {
			return backends.AdoptResult{Diverged: true, Divergences: []string{
				fmt.Sprintf("schedules[%d] (%q) cannot be translated to 5-field crontab", i, s),
			}}, nil
		}
		wanted = append(wanted, wantedSchedule{idx: i, manifest: s, fiveField: ff})
	}

	// Walk the file. For each candidate (a line whose command tail
	// matches expectedTail), record its cron expression and the line
	// index. Also detect AlreadyManaged early.
	type candidate struct {
		lineIndex int
		fiveField string
	}
	var candidates []candidate
	for i, line := range lines {
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		// AlreadyManaged: the next line is the cronix owner marker.
		// Skip — we don't double-count managed entries as candidates.
		if i+1 < len(lines) && ownerLineRe.MatchString(lines[i+1]) {
			continue
		}
		cron, tail, ok := splitScheduleAndCommand(stripped)
		if !ok {
			continue
		}
		if tail != expectedTail {
			continue
		}
		candidates = append(candidates, candidate{lineIndex: i, fiveField: cron})
	}

	// Detect entries already cronix-managed.
	managed, err := b.List(context.Background())
	if err != nil {
		return backends.AdoptResult{}, err
	}
	managedForJob := 0
	for _, m := range managed {
		if m.App == app && m.Job == job.Name {
			managedForJob++
		}
	}
	if managedForJob == len(wanted) && len(candidates) == 0 {
		// Everything is already adopted; nothing to do.
		return backends.AdoptResult{AlreadyManaged: true}, nil
	}

	// Match each wanted schedule to a candidate by 5-field cron.
	// Each candidate can only satisfy one wanted schedule
	// (handled by the consumed bitmap).
	consumed := make([]bool, len(candidates))
	matched := make([]int, len(wanted)) // index into candidates; -1 = unmatched
	for i := range matched {
		matched[i] = -1
	}
	for wIdx, w := range wanted {
		for cIdx, c := range candidates {
			if consumed[cIdx] {
				continue
			}
			if c.fiveField == w.fiveField {
				matched[wIdx] = cIdx
				consumed[cIdx] = true
				break
			}
		}
	}

	// Build divergence report.
	var divergences []string
	unmatchedWanted := 0
	for wIdx, w := range wanted {
		if matched[wIdx] < 0 {
			unmatchedWanted++
			divergences = append(divergences, fmt.Sprintf(
				"schedules[%d] (%q → %q): no candidate crontab line with this 5-field cron",
				w.idx, w.manifest, w.fiveField,
			))
		}
	}
	// Surface extras: candidates that exist on the host but don't
	// match any manifest schedule. These are likely stale or
	// hand-edited variants and would be left dangling on adopt;
	// surface them so the operator can prune.
	extraCandidates := 0
	for cIdx, c := range candidates {
		if !consumed[cIdx] {
			extraCandidates++
			divergences = append(divergences, fmt.Sprintf(
				"line %d (cron %q) invokes %s but does not match any manifest schedule",
				c.lineIndex+1, c.fiveField, expectedTail,
			))
		}
	}

	found := len(candidates) > 0
	if unmatchedWanted > 0 || extraCandidates > 0 {
		return backends.AdoptResult{
			Found:       found,
			Diverged:    true,
			Divergences: divergences,
		}, nil
	}

	// All wanted schedules have matching candidates; build the
	// post-adopt ManagedEntry list.
	entries := make([]backends.ManagedEntry, 0, len(wanted))
	for wIdx, w := range wanted {
		c := candidates[matched[wIdx]]
		entries = append(entries, backends.ManagedEntry{
			App:   app,
			Job:   job.Name,
			Hash:  policy.Hash(job, w.idx),
			Index: w.idx,
			Raw:   lines[c.lineIndex],
		})
	}

	if opts.DryRun {
		return backends.AdoptResult{Found: true, Entries: entries}, nil
	}

	// Apply: insert the owner marker line directly after each adopted
	// schedule line. Because we're inserting (not removing), iterate
	// in reverse so the lineIndex values stay valid as we mutate.
	err = b.rewrite(func(in []string) []string {
		out := make([]string, len(in))
		copy(out, in)
		// Collect insertions (lineIndex, marker text) and apply in
		// descending order so earlier indexes don't shift.
		type insertion struct {
			after  int
			marker string
		}
		ins := make([]insertion, 0, len(wanted))
		for wIdx, w := range wanted {
			c := candidates[matched[wIdx]]
			ins = append(ins, insertion{
				after:  c.lineIndex,
				marker: fmt.Sprintf("%s app=%s job=%s hash=%s idx=%d", ownerMarker, app, job.Name, policy.Hash(job, w.idx), w.idx),
			})
		}
		// Sort descending by `after`.
		for i := 0; i < len(ins); i++ {
			for j := i + 1; j < len(ins); j++ {
				if ins[j].after > ins[i].after {
					ins[i], ins[j] = ins[j], ins[i]
				}
			}
		}
		for _, ix := range ins {
			out = append(out[:ix.after+1], append([]string{ix.marker}, out[ix.after+1:]...)...)
		}
		return out
	})
	if err != nil {
		return backends.AdoptResult{}, err
	}
	return backends.AdoptResult{Found: true, Adopted: true, Entries: entries}, nil
}

// splitScheduleAndCommand splits a crontab line into "<5-field cron>"
// and "<command tail>". Returns ok=false for lines that don't have at
// least 6 whitespace-separated fields.
//
// Crontab grammar varies (some files include a USER field, some have
// environment variable assignments), so this is intentionally minimal:
// only honors the 5-cron-fields + command form. Lines that don't fit
// are simply not adoption candidates.
func splitScheduleAndCommand(line string) (cron, command string, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return "", "", false
	}
	// Crontab schedule fields can contain commas, ranges, steps — but
	// never whitespace. The first 5 whitespace-separated fields are the
	// schedule; everything after is the command.
	cron = strings.Join(fields[:5], " ")
	// Preserve internal whitespace in the command; can't just
	// strings.Join(fields[5:]) because that would collapse spaces.
	// Find the position of fields[5] in the original line.
	idx := 0
	for fIdx, f := range fields {
		if fIdx == 5 {
			break
		}
		j := strings.Index(line[idx:], f)
		if j < 0 {
			return "", "", false
		}
		idx += j + len(f)
	}
	command = strings.TrimSpace(line[idx:])
	return cron, command, true
}
