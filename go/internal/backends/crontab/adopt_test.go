package crontab

import (
	"context"
	"strings"
	"testing"

	"github.com/awbx/cronix/go/internal/backends"
)

func TestAdopt_SingleScheduleSuccess(t *testing.T) {
	initial := "# user comment\n0 * * * * /usr/local/bin/cronix trigger billing.ping\n"
	b := newBackend(t, initial)

	res, err := b.Adopt(context.Background(), "billing", sampleJob("ping", "@hourly"), backends.AdoptOpts{})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if !res.Adopted {
		t.Fatalf("Adopted=false, want true. Result: %+v", res)
	}
	if res.Diverged {
		t.Fatalf("Diverged=true, want false. Divergences: %v", res.Divergences)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(res.Entries))
	}

	got := read(t, b)
	if !strings.Contains(got, "# user comment") {
		t.Errorf("user comment lost:\n%s", got)
	}
	if !strings.Contains(got, "0 * * * * /usr/local/bin/cronix trigger billing.ping") {
		t.Errorf("schedule line altered:\n%s", got)
	}
	if !strings.Contains(got, "# cronix:owned app=billing job=ping hash=") {
		t.Errorf("ownership marker missing:\n%s", got)
	}
	// Critical: the original line was NOT removed and re-created. It
	// stays in place, with the marker appended after it.
	if strings.Count(got, "/usr/local/bin/cronix trigger billing.ping") != 1 {
		t.Errorf("schedule line duplicated:\n%s", got)
	}
}

func TestAdopt_DryRunDoesNotModify(t *testing.T) {
	initial := "0 * * * * /usr/local/bin/cronix trigger billing.ping\n"
	b := newBackend(t, initial)

	res, err := b.Adopt(context.Background(), "billing", sampleJob("ping", "@hourly"), backends.AdoptOpts{DryRun: true})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if !res.Found {
		t.Fatalf("Found=false, want true")
	}
	if res.Adopted {
		t.Fatalf("Adopted=true under DryRun, want false")
	}
	got := read(t, b)
	if got != initial {
		t.Errorf("file modified under dry-run:\nbefore: %q\nafter:  %q", initial, got)
	}
}

func TestAdopt_DivergentScheduleNoChange(t *testing.T) {
	// Existing line fires every 5 minutes; manifest says hourly. Adopt
	// must NOT modify (would silently change firing cadence).
	initial := "*/5 * * * * /usr/local/bin/cronix trigger billing.ping\n"
	b := newBackend(t, initial)

	res, err := b.Adopt(context.Background(), "billing", sampleJob("ping", "@hourly"), backends.AdoptOpts{})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if !res.Diverged {
		t.Fatalf("Diverged=false, want true. Result: %+v", res)
	}
	if res.Adopted {
		t.Fatalf("Adopted=true on divergent entry, want false")
	}
	got := read(t, b)
	if got != initial {
		t.Errorf("file modified despite divergence:\n%s", got)
	}
	// Both divergences should surface: wanted-no-match + extra-candidate.
	joined := strings.Join(res.Divergences, "\n")
	if !strings.Contains(joined, "no candidate crontab line with this 5-field cron") {
		t.Errorf("missing wanted-unmatched divergence:\n%s", joined)
	}
	if !strings.Contains(joined, "does not match any manifest schedule") {
		t.Errorf("missing extra-candidate divergence:\n%s", joined)
	}
}

func TestAdopt_AlreadyManagedIsNoOp(t *testing.T) {
	b := newBackend(t, "")
	job := sampleJob("ping", "@hourly")
	if err := b.Create(context.Background(), "billing", job); err != nil {
		t.Fatalf("create: %v", err)
	}
	before := read(t, b)

	res, err := b.Adopt(context.Background(), "billing", job, backends.AdoptOpts{})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if !res.AlreadyManaged {
		t.Errorf("AlreadyManaged=false, want true. Result: %+v", res)
	}
	if res.Adopted {
		t.Errorf("Adopted=true on already-managed, want false (it was a no-op)")
	}
	if read(t, b) != before {
		t.Errorf("file changed on already-managed adopt")
	}
}

func TestAdopt_NoCandidateReportsNotFound(t *testing.T) {
	// Crontab has lines but none invoke the expected trigger command.
	initial := "0 * * * * /opt/some-other-script.sh\n0 0 * * * /usr/bin/echo hi\n"
	b := newBackend(t, initial)

	res, err := b.Adopt(context.Background(), "billing", sampleJob("ping", "@hourly"), backends.AdoptOpts{})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if res.Found {
		t.Errorf("Found=true, want false (no matching trigger lines)")
	}
	if !res.Diverged {
		t.Errorf("expected Diverged=true (manifest schedule has no candidate)")
	}
}

func TestAdopt_MultiScheduleAllPresent(t *testing.T) {
	initial := strings.Join([]string{
		"# day shift",
		"0 9 * * * /usr/local/bin/cronix trigger billing.ping",
		"# night shift",
		"0 21 * * * /usr/local/bin/cronix trigger billing.ping",
		"",
	}, "\n")
	b := newBackend(t, initial)
	job := sampleJob("ping", "0 9 * * *", "0 21 * * *")

	res, err := b.Adopt(context.Background(), "billing", job, backends.AdoptOpts{})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if !res.Adopted || res.Diverged {
		t.Fatalf("expected clean adopt, got: %+v", res)
	}
	if len(res.Entries) != 2 {
		t.Errorf("len(Entries) = %d, want 2", len(res.Entries))
	}
	got := read(t, b)
	if strings.Count(got, "# cronix:owned app=billing job=ping") != 2 {
		t.Errorf("expected 2 owner markers, got:\n%s", got)
	}
	// Both schedule lines preserved, neither moved.
	if !strings.Contains(got, "0 9 * * * /usr/local/bin/cronix trigger billing.ping") {
		t.Errorf("day-shift line missing:\n%s", got)
	}
	if !strings.Contains(got, "0 21 * * * /usr/local/bin/cronix trigger billing.ping") {
		t.Errorf("night-shift line missing:\n%s", got)
	}
}

func TestAdopt_PartialMultiScheduleIsDivergent(t *testing.T) {
	// Manifest wants two schedules but only one exists on host.
	initial := "0 9 * * * /usr/local/bin/cronix trigger billing.ping\n"
	b := newBackend(t, initial)
	job := sampleJob("ping", "0 9 * * *", "0 21 * * *")

	res, err := b.Adopt(context.Background(), "billing", job, backends.AdoptOpts{})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if !res.Diverged {
		t.Errorf("expected Diverged=true (1 of 2 schedules missing)")
	}
	if res.Adopted {
		t.Errorf("expected Adopted=false (partial match)")
	}
	if read(t, b) != initial {
		t.Errorf("file modified on partial adopt")
	}
}

func TestAdopt_AdoptInterfaceCompliance(t *testing.T) {
	// Compile-time check that *Backend satisfies backends.Adopter.
	var _ backends.Adopter = (*Backend)(nil)
}
