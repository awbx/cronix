// Package manifest parses, validates, and normalizes cronix manifests.
//
// The on-the-wire shape and normalization rules are defined by spec/RFC.md
// and exhaustively tested against spec/manifest-vectors.json. This package
// must agree with the @cronix/sdk TypeScript implementation byte-for-byte
// on every vector.
package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Defaults — keep in sync with packages/sdk/src/core/manifest.ts.
const (
	TimeoutMin             = 1
	TimeoutMax             = 600
	TimeoutDefault         = 60
	RetryAttemptsMin       = 1
	RetryAttemptsMax       = 10
	RetryAttemptsDefault   = 3
	RetryBackoffMinDefault = 1
	RetryBackoffMaxDefault = 60
	ReplayWindowDefault    = 300
	MaxJobs                = 256
	MaxSchedulesPerJob     = 64
	MaxSecretRefs          = 8
)

var (
	jobNameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	appIDRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	secretRefRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_:./-]{0,127}$`)
	httpMethods = map[string]struct{}{"GET": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {}}
	concurrency = map[string]struct{}{"Allow": {}, "Forbid": {}, "Replace": {}}
	concScope   = map[string]struct{}{"host": {}, "global": {}}
)

// Manifest is the raw parsed shape (post-JSON, pre-normalization).
type Manifest struct {
	Version int    `json:"version"`
	App     string `json:"app"`
	Jobs    []Job  `json:"jobs"`
}

type Job struct {
	Name      string   `json:"name"`
	Schedule  *string  `json:"schedule,omitempty"`
	Schedules []string `json:"schedules,omitempty"`
	Timezone  *string  `json:"timezone,omitempty"`
	Request   Request  `json:"request"`
	Policy    *Policy  `json:"policy,omitempty"`
	Auth      *Auth    `json:"auth,omitempty"`
}

type Request struct {
	Method  *string           `json:"method,omitempty"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    *string           `json:"body,omitempty"`
}

type Policy struct {
	Concurrency      *string  `json:"concurrency,omitempty"`
	ConcurrencyScope *string  `json:"concurrency_scope,omitempty"`
	TimeoutSeconds   *int     `json:"timeout_seconds,omitempty"`
	Retries          *Retries `json:"retries,omitempty"`
}

type Retries struct {
	MaxAttempts *int `json:"max_attempts,omitempty"`
	MinSeconds  *int `json:"min_seconds,omitempty"`
	MaxSeconds  *int `json:"max_seconds,omitempty"`
}

type Auth struct {
	SecretRefs []string `json:"secret_refs,omitempty"`
}

// NormalizedManifest is the post-defaults shape — every field is set.
// The JSON tag order on these structs matches the field order used by the
// canonicalize() function in @cronix/sdk so encoding/json produces the
// same byte sequence.
type NormalizedManifest struct {
	Version int             `json:"version"`
	App     string          `json:"app"`
	Jobs    []NormalizedJob `json:"jobs"`
}

type NormalizedJob struct {
	Name      string            `json:"name"`
	Schedules []string          `json:"schedules"`
	Timezone  string            `json:"timezone"`
	Request   NormalizedRequest `json:"request"`
	Policy    NormalizedPolicy  `json:"policy"`
	Auth      NormalizedAuth    `json:"auth"`
}

type NormalizedRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type NormalizedPolicy struct {
	Concurrency      string            `json:"concurrency"`
	ConcurrencyScope string            `json:"concurrency_scope"`
	TimeoutSeconds   int               `json:"timeout_seconds"`
	Retries          NormalizedRetries `json:"retries"`
}

type NormalizedRetries struct {
	MaxAttempts int `json:"max_attempts"`
	MinSeconds  int `json:"min_seconds"`
	MaxSeconds  int `json:"max_seconds"`
}

type NormalizedAuth struct {
	SecretRefs []string `json:"secret_refs"`
}

// Issue is a single validation problem.
type Issue struct {
	Path    []string `json:"path"`
	Message string   `json:"message"`
	Code    string   `json:"code"`
}

// Error is the aggregated validation failure.
type Error struct {
	Issues []Issue
}

func (e *Error) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "manifest invalid"
	}
	parts := make([]string, len(e.Issues))
	for i, is := range e.Issues {
		parts[i] = fmt.Sprintf("%s: %s", strings.Join(is.Path, "/"), is.Message)
	}
	return "manifest invalid: " + strings.Join(parts, "; ")
}

// Parse decodes raw JSON into a Manifest, then validates it. Returns the
// parsed manifest and an *Error on validation failure.
//
// JSON decode errors are surfaced as a single issue at the root path with
// code "decode".
func Parse(raw []byte) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, &Error{Issues: []Issue{{Path: []string{}, Message: err.Error(), Code: "decode"}}}
	}
	if err := Validate(&m); err != nil {
		return &m, err
	}
	return &m, nil
}

// Validate runs all structural and semantic checks on the parsed manifest.
func Validate(m *Manifest) error {
	v := &validator{}
	if m.Version != 1 {
		v.add([]string{"version"}, "version must be 1", "invalid_value")
	}
	if !appIDRe.MatchString(m.App) {
		v.add([]string{"app"}, "app id must match ^[a-z][a-z0-9-]{0,62}$", "invalid_format")
	}
	if len(m.Jobs) == 0 {
		v.add([]string{"jobs"}, "must contain at least one job", "too_small")
	}
	if len(m.Jobs) > MaxJobs {
		v.add([]string{"jobs"}, fmt.Sprintf("must contain at most %d jobs", MaxJobs), "too_big")
	}
	seen := make(map[string]int, len(m.Jobs))
	for i, j := range m.Jobs {
		path := []string{"jobs", fmt.Sprintf("%d", i)}
		v.validateJob(path, j)
		if prev, ok := seen[j.Name]; ok && j.Name != "" {
			v.add(append(path, "name"), fmt.Sprintf("duplicate job name: %s (also at jobs/%d)", j.Name, prev), "custom")
		} else if j.Name != "" {
			seen[j.Name] = i
		}
	}
	if len(v.issues) > 0 {
		return &Error{Issues: v.issues}
	}
	return nil
}

type validator struct{ issues []Issue }

func (v *validator) add(path []string, message, code string) {
	v.issues = append(v.issues, Issue{Path: append([]string(nil), path...), Message: message, Code: code})
}

func (v *validator) validateJob(path []string, j Job) {
	if !jobNameRe.MatchString(j.Name) {
		v.add(append(append([]string(nil), path...), "name"), "job name must match ^[a-z][a-z0-9-]{0,62}$", "invalid_format")
	}

	hasSchedule := j.Schedule != nil
	hasSchedules := j.Schedules != nil
	if hasSchedule == hasSchedules {
		v.add(append([]string(nil), path...), "exactly one of `schedule` or `schedules` must be set", "custom")
	}
	if hasSchedule {
		if e := validateSchedule(*j.Schedule); e != "" {
			v.add(append(append([]string(nil), path...), "schedule"), e, "custom")
		}
	}
	if hasSchedules {
		if len(j.Schedules) == 0 {
			v.add(append(append([]string(nil), path...), "schedules"), "must contain at least one schedule", "too_small")
		} else if len(j.Schedules) > MaxSchedulesPerJob {
			v.add(append(append([]string(nil), path...), "schedules"), fmt.Sprintf("must contain at most %d schedules", MaxSchedulesPerJob), "too_big")
		}
		for si, s := range j.Schedules {
			if e := validateSchedule(s); e != "" {
				v.add(append(append([]string(nil), path...), "schedules", fmt.Sprintf("%d", si)), e, "custom")
			}
		}
	}

	v.validateRequest(append(append([]string(nil), path...), "request"), j.Request)
	if j.Policy != nil {
		v.validatePolicy(append(append([]string(nil), path...), "policy"), *j.Policy)
	}
	if j.Auth != nil {
		v.validateAuth(append(append([]string(nil), path...), "auth"), *j.Auth)
	}
}

func (v *validator) validateRequest(path []string, r Request) {
	if r.URL == "" {
		v.add(append(append([]string(nil), path...), "url"), "url is required", "invalid_value")
	} else {
		u, err := url.Parse(r.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			v.add(append(append([]string(nil), path...), "url"), "url must be http(s)", "invalid_format")
		}
	}
	if r.Method != nil {
		if _, ok := httpMethods[*r.Method]; !ok {
			v.add(append(append([]string(nil), path...), "method"), "method must be one of GET POST PUT PATCH DELETE", "invalid_value")
		}
	}
}

func (v *validator) validatePolicy(path []string, p Policy) {
	if p.Concurrency != nil {
		if _, ok := concurrency[*p.Concurrency]; !ok {
			v.add(append(append([]string(nil), path...), "concurrency"), "concurrency must be Allow|Forbid|Replace", "invalid_value")
		}
	}
	if p.ConcurrencyScope != nil {
		if _, ok := concScope[*p.ConcurrencyScope]; !ok {
			v.add(append(append([]string(nil), path...), "concurrency_scope"), "concurrency_scope must be host|global", "invalid_value")
		}
	}
	if p.TimeoutSeconds != nil {
		if *p.TimeoutSeconds < TimeoutMin || *p.TimeoutSeconds > TimeoutMax {
			v.add(append(append([]string(nil), path...), "timeout_seconds"), fmt.Sprintf("timeout_seconds must be between %d and %d", TimeoutMin, TimeoutMax), "out_of_range")
		}
	}
	if p.Retries != nil {
		v.validateRetries(append(append([]string(nil), path...), "retries"), *p.Retries)
	}
}

func (v *validator) validateRetries(path []string, r Retries) {
	if r.MaxAttempts != nil && (*r.MaxAttempts < RetryAttemptsMin || *r.MaxAttempts > RetryAttemptsMax) {
		v.add(append(append([]string(nil), path...), "max_attempts"), fmt.Sprintf("max_attempts must be between %d and %d", RetryAttemptsMin, RetryAttemptsMax), "out_of_range")
	}
	if r.MinSeconds != nil && *r.MinSeconds < 0 {
		v.add(append(append([]string(nil), path...), "min_seconds"), "min_seconds must be >= 0", "out_of_range")
	}
	if r.MaxSeconds != nil && *r.MaxSeconds < 1 {
		v.add(append(append([]string(nil), path...), "max_seconds"), "max_seconds must be >= 1", "out_of_range")
	}
	if r.MinSeconds != nil && r.MaxSeconds != nil && *r.MinSeconds > *r.MaxSeconds {
		v.add(append([]string(nil), path...), "retries.min_seconds must be <= retries.max_seconds", "custom")
	}
}

func (v *validator) validateAuth(path []string, a Auth) {
	if a.SecretRefs != nil {
		if len(a.SecretRefs) == 0 {
			v.add(append(append([]string(nil), path...), "secret_refs"), "must contain at least one secret_ref", "too_small")
		}
		if len(a.SecretRefs) > MaxSecretRefs {
			v.add(append(append([]string(nil), path...), "secret_refs"), fmt.Sprintf("must contain at most %d secret_refs", MaxSecretRefs), "too_big")
		}
		for i, s := range a.SecretRefs {
			if !secretRefRe.MatchString(s) {
				v.add(append(append([]string(nil), path...), "secret_refs", fmt.Sprintf("%d", i)), "invalid secret ref format", "invalid_format")
			}
		}
	}
}

// ApplyDefaults fills every optional field with its documented default and
// returns the canonical (sorted, deterministic) NormalizedManifest.
//
// Caller must Validate first; ApplyDefaults assumes valid input.
func ApplyDefaults(m *Manifest) *NormalizedManifest {
	jobs := make([]NormalizedJob, len(m.Jobs))
	for i, j := range m.Jobs {
		var schedules []string
		if j.Schedules != nil {
			schedules = make([]string, len(j.Schedules))
			for k, s := range j.Schedules {
				schedules[k] = strings.TrimSpace(s)
			}
		} else if j.Schedule != nil {
			schedules = []string{strings.TrimSpace(*j.Schedule)}
		} else {
			schedules = []string{}
		}

		method := "POST"
		if j.Request.Method != nil {
			method = *j.Request.Method
		}
		body := ""
		if j.Request.Body != nil {
			body = *j.Request.Body
		}
		headers := sortedHeaders(j.Request.Headers)

		var p Policy
		if j.Policy != nil {
			p = *j.Policy
		}
		conc := "Forbid"
		if p.Concurrency != nil {
			conc = *p.Concurrency
		}
		scope := "host"
		if p.ConcurrencyScope != nil {
			scope = *p.ConcurrencyScope
		}
		timeout := TimeoutDefault
		if p.TimeoutSeconds != nil {
			timeout = *p.TimeoutSeconds
		}
		var r Retries
		if p.Retries != nil {
			r = *p.Retries
		}
		retries := NormalizedRetries{
			MaxAttempts: defaultInt(r.MaxAttempts, RetryAttemptsDefault),
			MinSeconds:  defaultInt(r.MinSeconds, RetryBackoffMinDefault),
			MaxSeconds:  defaultInt(r.MaxSeconds, RetryBackoffMaxDefault),
		}

		var secrets []string
		if j.Auth != nil && j.Auth.SecretRefs != nil {
			secrets = append([]string(nil), j.Auth.SecretRefs...)
		} else {
			secrets = []string{}
		}

		tz := "UTC"
		if j.Timezone != nil {
			tz = *j.Timezone
		}

		jobs[i] = NormalizedJob{
			Name:      j.Name,
			Schedules: schedules,
			Timezone:  tz,
			Request: NormalizedRequest{
				Method:  method,
				URL:     j.Request.URL,
				Headers: headers,
				Body:    body,
			},
			Policy: NormalizedPolicy{
				Concurrency:      conc,
				ConcurrencyScope: scope,
				TimeoutSeconds:   timeout,
				Retries:          retries,
			},
			Auth: NormalizedAuth{SecretRefs: secrets},
		}
	}
	sort.SliceStable(jobs, func(i, k int) bool { return jobs[i].Name < jobs[k].Name })

	return &NormalizedManifest{
		Version: 1,
		App:     m.App,
		Jobs:    jobs,
	}
}

func defaultInt(p *int, d int) int {
	if p == nil {
		return d
	}
	return *p
}

func sortedHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// encoding/json sorts map keys alphabetically when marshalling, so a
	// regular map is enough — but we copy into a fresh map anyway to make
	// the contract explicit and avoid sharing the caller's map.
	out := make(map[string]string, len(in))
	for _, k := range keys {
		out[k] = in[k]
	}
	return out
}

// Canonicalize returns the byte-exact JSON serialization required for
// cross-implementation hash equality. Two implementations must produce
// identical bytes for the same NormalizedManifest. Object keys are emitted
// in struct-tag order; encoding/json sorts map keys (headers) alphabetically;
// arrays preserve their (already-sorted) order.
func Canonicalize(m *NormalizedManifest) ([]byte, error) {
	if m == nil {
		return nil, errors.New("nil manifest")
	}
	return json.Marshal(m)
}

// MustCanonicalize is the panic-on-error variant for tests.
func MustCanonicalize(m *NormalizedManifest) []byte {
	b, err := Canonicalize(m)
	if err != nil {
		panic(err)
	}
	return b
}
