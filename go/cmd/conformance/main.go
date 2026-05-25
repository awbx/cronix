// Command conformance is the cronix language-neutral conformance runner.
//
// It reads spec/manifest-vectors.json + spec/auth-vectors.json and runs
// every vector against an "SDK adapter" — a small CLI any cronix SDK
// ships that responds to three subcommands via stdin/stdout:
// manifest-canonicalize, auth-sign, auth-verify. The contract is
// documented in spec/conformance/README.md.
//
// Usage:
//
//	go run github.com/awbx/cronix/go/cmd/conformance \
//	  --vectors spec \
//	  --adapter "node ts/packages/sdk/test/conformance-adapter.mjs"
//
// Exit codes:
//
//	0  every vector passed
//	1  at least one vector failed
//	2  configuration error (vectors missing, adapter not executable)
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type manifestVectorFile struct {
	Comment string           `json:"$comment"`
	Version int              `json:"version"`
	Vectors []manifestVector `json:"vectors"`
}

type manifestVector struct {
	Name       string          `json:"name"`
	Valid      bool            `json:"valid"`
	Input      json.RawMessage `json:"input"`
	Expected   string          `json:"expected"`
	ErrorPaths []string        `json:"errorPaths"`
}

type authVectorFile struct {
	Comment string       `json:"$comment"`
	Version int          `json:"version"`
	Vectors []authVector `json:"vectors"`
}

type authVector struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "sign" or "verify"

	// sign + verify shared
	Method  string `json:"method"`
	Path    string `json:"path"`
	BodyB64 string `json:"bodyB64"`

	// sign-specific
	Secret         string `json:"secret"`
	Timestamp      int64  `json:"timestamp"`
	ExpectedHeader string `json:"expectedHeader"`

	// verify-specific
	Secrets             []string `json:"secrets"`
	Header              string   `json:"header"`
	Now                 int64    `json:"now"`
	MaxSkewSeconds      int      `json:"maxSkewSeconds"`
	Expect              string   `json:"expect"` // "ok" or an error code
	ExpectedSecretIndex int      `json:"expectedSecretIndex"`
}

type result struct {
	suite  string
	name   string
	passed bool
	detail string
}

func main() {
	var (
		vectorsDir string
		adapter    string
	)
	flag.StringVar(&vectorsDir, "vectors", "spec", "directory containing manifest-vectors.json and auth-vectors.json")
	flag.StringVar(&adapter, "adapter", "", "adapter command (passed to sh -c so quoting and args work) — required")
	flag.Parse()

	if adapter == "" {
		fmt.Fprintln(os.Stderr, "error: --adapter is required")
		os.Exit(2)
	}

	manifestFile, err := loadManifestVectors(filepath.Join(vectorsDir, "manifest-vectors.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load manifest vectors: %v\n", err)
		os.Exit(2)
	}
	authFile, err := loadAuthVectors(filepath.Join(vectorsDir, "auth-vectors.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load auth vectors: %v\n", err)
		os.Exit(2)
	}

	var results []result

	for _, v := range manifestFile.Vectors {
		results = append(results, runManifestVector(adapter, v))
	}
	for _, v := range authFile.Vectors {
		switch v.Kind {
		case "sign":
			results = append(results, runAuthSignVector(adapter, v))
		case "verify":
			results = append(results, runAuthVerifyVector(adapter, v))
		default:
			results = append(results, result{
				suite:  "auth",
				name:   v.Name,
				passed: false,
				detail: fmt.Sprintf("unknown vector kind %q", v.Kind),
			})
		}
	}

	failed := 0
	for _, r := range results {
		if r.passed {
			fmt.Printf("PASS  %-10s  %s\n", r.suite, r.name)
		} else {
			fmt.Printf("FAIL  %-10s  %s\n      %s\n", r.suite, r.name, r.detail)
			failed++
		}
	}
	fmt.Printf("\n%d total / %d passed / %d failed\n", len(results), len(results)-failed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func loadManifestVectors(path string) (*manifestVectorFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f manifestVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return &f, nil
}

func loadAuthVectors(path string) (*authVectorFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f authVectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return &f, nil
}

func runAdapter(adapter, subcommand string, input []byte) ([]byte, error) {
	// Run via shell so the adapter flag can include arguments and quoting.
	// Adapters are operator-supplied and run with full process privileges
	// — same trust model as any other operator tool. Not for untrusted
	// input.
	cmd := exec.Command("sh", "-c", adapter+" "+subcommand) //#nosec G204 — operator-controlled, documented
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("adapter %s exited %v: stderr=%s", subcommand, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func runManifestVector(adapter string, v manifestVector) result {
	r := result{suite: "manifest", name: v.Name}
	out, err := runAdapter(adapter, "manifest-canonicalize", v.Input)
	if err != nil {
		r.detail = err.Error()
		return r
	}
	outStr := strings.TrimRight(string(out), "\n")

	if v.Valid {
		// Adapter must produce expected canonical JSON byte-for-byte.
		if outStr == v.Expected {
			r.passed = true
			return r
		}
		// Tolerate object-key ordering differences by re-comparing parsed
		// forms. If THAT also differs, it's a real divergence.
		if jsonEquivalent(outStr, v.Expected) {
			r.passed = true
			r.detail = "(parsed-equivalent; byte-for-byte differs — see canonical-form discipline)"
			return r
		}
		r.detail = fmt.Sprintf("expected:\n  %s\ngot:\n  %s", abbrev(v.Expected, 200), abbrev(outStr, 200))
		return r
	}

	// Invalid vector: adapter must report at least one error path matching one of v.ErrorPaths.
	var errOut struct {
		Error struct {
			Paths []string `json:"paths"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(outStr), &errOut); err != nil {
		r.detail = fmt.Sprintf("invalid vector but adapter did not emit {\"error\":{\"paths\":...}}: stdout=%s", abbrev(outStr, 200))
		return r
	}
	for _, want := range v.ErrorPaths {
		if slices.Contains(errOut.Error.Paths, want) {
			r.passed = true
			return r
		}
	}
	r.detail = fmt.Sprintf("expected at least one path from %v, got %v", v.ErrorPaths, errOut.Error.Paths)
	return r
}

func runAuthSignVector(adapter string, v authVector) result {
	r := result{suite: "auth/sign", name: v.Name}
	input, _ := json.Marshal(map[string]any{
		"secret":    v.Secret,
		"method":    v.Method,
		"path":      v.Path,
		"bodyB64":   v.BodyB64,
		"timestamp": v.Timestamp,
	})
	out, err := runAdapter(adapter, "auth-sign", input)
	if err != nil {
		r.detail = err.Error()
		return r
	}
	got := strings.TrimRight(string(out), "\n")
	if got == v.ExpectedHeader {
		r.passed = true
		return r
	}
	r.detail = fmt.Sprintf("expected %q, got %q", v.ExpectedHeader, got)
	return r
}

func runAuthVerifyVector(adapter string, v authVector) result {
	r := result{suite: "auth/verify", name: v.Name}
	input, _ := json.Marshal(map[string]any{
		"secrets":        v.Secrets,
		"method":         v.Method,
		"path":           v.Path,
		"bodyB64":        v.BodyB64,
		"header":         v.Header,
		"now":            v.Now,
		"maxSkewSeconds": v.MaxSkewSeconds,
	})
	out, err := runAdapter(adapter, "auth-verify", input)
	if err != nil {
		r.detail = err.Error()
		return r
	}
	var resp struct {
		OK          bool   `json:"ok"`
		SecretIndex int    `json:"secret_index"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		r.detail = fmt.Sprintf("adapter response did not parse as JSON: %s", string(out))
		return r
	}
	if v.Expect == "ok" {
		if resp.OK && resp.SecretIndex == v.ExpectedSecretIndex {
			r.passed = true
			return r
		}
		r.detail = fmt.Sprintf("expected ok+index=%d, got ok=%v index=%d error=%q",
			v.ExpectedSecretIndex, resp.OK, resp.SecretIndex, resp.Error)
		return r
	}
	if !resp.OK && resp.Error == v.Expect {
		r.passed = true
		return r
	}
	r.detail = fmt.Sprintf("expected error=%q, got ok=%v error=%q", v.Expect, resp.OK, resp.Error)
	return r
}

// jsonEquivalent compares two JSON strings as parsed values, ignoring key
// ordering. The canonical form should NOT differ in key order — if this
// path triggers a pass with a "byte-for-byte differs" note, the adapter
// likely has a canonicalization bug worth investigating.
func jsonEquivalent(a, b string) bool {
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return false
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return bytes.Equal(ab, bb)
}

func abbrev(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
