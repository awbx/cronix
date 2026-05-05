package systemd

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/awbx/cronix/go/internal/backends"
)

// Parsing regexes for the X-Cronix-* annotations cronix writes into
// every owned unit. Built once at init from the annotation constants in
// policy.go so renaming an annotation flows through here automatically.
var (
	appLineRe           = mustAnnotation(annotationApp)
	jobLineRe           = mustAnnotation(annotationJob)
	hashLineRe          = mustAnnotation(annotationHash)
	indexLineRe         = mustAnnotationDigits(annotationIndex)
	canonicalCalendarRe = mustAnnotation(annotationOnCalendar)
	actualCalendarRe    = regexp.MustCompile(`(?m)^OnCalendar=(.+)$`)
)

func mustAnnotation(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=(.+)$`)
}

func mustAnnotationDigits(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=(\d+)$`)
}

// parseUnit extracts a ManagedEntry from a unit file's X-Cronix-*
// annotations. Returns ok=false for foreign units (no annotations) so
// callers can skip them without error.
func parseUnit(contents string) (backends.ManagedEntry, bool) {
	app := firstMatch(appLineRe, contents)
	job := firstMatch(jobLineRe, contents)
	idxStr := firstMatch(indexLineRe, contents)
	if app == "" || job == "" || idxStr == "" {
		return backends.ManagedEntry{}, false
	}
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		return backends.ManagedEntry{}, false
	}
	return backends.ManagedEntry{
		App:   app,
		Job:   job,
		Hash:  firstMatch(hashLineRe, contents),
		Index: idx,
	}, true
}

func firstMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}
