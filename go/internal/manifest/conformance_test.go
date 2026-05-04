package manifest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type rawVector struct {
	Name        string          `json:"name"`
	Valid       bool            `json:"valid"`
	Input       json.RawMessage `json:"input"`
	Expected    string          `json:"expected"`
	ErrorPaths  []string        `json:"errorPaths"`
}

type vectorFile struct {
	Vectors []rawVector `json:"vectors"`
}

func loadVectors(t *testing.T) []rawVector {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "spec", "manifest-vectors.json")
	b, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var f vectorFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}
	return f.Vectors
}

func TestManifestConformance(t *testing.T) {
	vectors := loadVectors(t)
	if len(vectors) == 0 {
		t.Skip("no vectors")
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			parsed, err := Parse([]byte(v.Input))
			if v.Valid {
				if err != nil {
					t.Fatalf("expected valid manifest, got error: %v", err)
				}
				normalized := ApplyDefaults(parsed)
				canonical, cerr := Canonicalize(normalized)
				if cerr != nil {
					t.Fatalf("canonicalize: %v", cerr)
				}
				if string(canonical) != v.Expected {
					t.Fatalf("canonical mismatch:\n got: %s\nwant: %s", canonical, v.Expected)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected validation error, got none")
			}
			var manifestErr *Error
			if !errors.As(err, &manifestErr) {
				t.Fatalf("expected *Error, got %T: %v", err, err)
			}
			reported := make(map[string]struct{})
			for _, is := range manifestErr.Issues {
				reported[strings.Join(is.Path, "/")] = struct{}{}
			}
			for _, want := range v.ErrorPaths {
				matched := false
				for path := range reported {
					if path == want || strings.HasPrefix(path, want+"/") {
						matched = true
						break
					}
				}
				if !matched {
					reportedList := make([]string, 0, len(reported))
					for p := range reported {
						reportedList = append(reportedList, p)
					}
					t.Fatalf("expected an issue at %q; reported paths: %v", want, reportedList)
				}
			}
		})
	}
}
