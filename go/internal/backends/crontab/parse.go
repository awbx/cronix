package crontab

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

var (
	// Identifier validation: lowercase letter or digit, no leading digit,
	// hyphens allowed, max 63 chars total. Mirrors the manifest's
	// app-id / job-name regex so a manifest that the SDK accepts cannot
	// produce a name the crontab backend rejects.
	appRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	jobRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

	// ownerLineRe matches a cronix ownership comment line. Capture
	// groups are named so callers don't depend on positional indices.
	ownerLineRe = regexp.MustCompile(
		`^` + regexp.QuoteMeta(ownerMarker) +
			` app=(?P<app>[a-z][a-z0-9-]{0,62}) job=(?P<job>[a-z][a-z0-9-]{0,62}) hash=(?P<hash>[0-9a-f]{1,64}) idx=(?P<idx>\d+)$`,
	)
)

// rawEntry is one parsed (schedule line, owner line) pair from the
// crontab. The schedule line is the actual cron entry; the owner line
// is the # cronix:owned annotation that follows it.
type rawEntry struct {
	scheduleLine string
	ownerLine    string
}

// parseLines reads the crontab from r and returns:
//   - entries: all cronix-owned (schedule, owner) pairs
//   - all: the raw line list, preserved for atomicWrite
func parseLines(r io.Reader) ([]rawEntry, []string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var (
		all     []string
		entries []rawEntry
		prev    string
	)
	for scanner.Scan() {
		line := scanner.Text()
		all = append(all, line)
		if ownerLineRe.MatchString(line) && prev != "" && !strings.HasPrefix(prev, "#") {
			entries = append(entries, rawEntry{scheduleLine: prev, ownerLine: line})
		}
		prev = line
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return entries, all, nil
}

// stripOwnedFor returns lines minus any 2-line owned block for (app, job).
// Foreign lines are preserved in original order.
func stripOwnedFor(lines []string, app, job string) []string {
	out := make([]string, 0, len(lines))
	for i := range lines {
		m := ownerLineRe.FindStringSubmatch(lines[i])
		if m != nil && m[1] == app && m[2] == job {
			// Drop the schedule line that immediately precedes this marker.
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, lines[i])
	}
	return out
}
