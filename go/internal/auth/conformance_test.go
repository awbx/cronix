package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type vector struct {
	Name string `json:"name"`
	Kind string `json:"kind"`

	// sign
	Secret         string `json:"secret"`
	ExpectedHeader string `json:"expectedHeader"`
	Timestamp      int64  `json:"timestamp"`

	// shared
	Method  string `json:"method"`
	Path    string `json:"path"`
	BodyB64 string `json:"bodyB64"`

	// verify
	Secrets             []string `json:"secrets"`
	Header              string   `json:"header"`
	Now                 int64    `json:"now"`
	MaxSkewSeconds      int      `json:"maxSkewSeconds"`
	Expect              string   `json:"expect"`
	ExpectedSecretIndex int      `json:"expectedSecretIndex"`
}

type vectorFile struct {
	Vectors []vector `json:"vectors"`
}

func loadAuthVectors(t *testing.T) []vector {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "spec", "auth-vectors.json")
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

func TestAuthConformance(t *testing.T) {
	for _, v := range loadAuthVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			body, err := base64.StdEncoding.DecodeString(v.BodyB64)
			if err != nil {
				t.Fatalf("decode bodyB64: %v", err)
			}

			switch v.Kind {
			case "sign":
				out, signErr := Sign(SignOptions{
					Secret:    v.Secret,
					Method:    v.Method,
					Path:      v.Path,
					Body:      body,
					Timestamp: v.Timestamp,
				})
				if signErr != nil {
					t.Fatalf("sign: %v", signErr)
				}
				if out.Header != v.ExpectedHeader {
					t.Fatalf("header mismatch:\n got: %s\nwant: %s", out.Header, v.ExpectedHeader)
				}

			case "verify":
				result, verifyErr := Verify(VerifyOptions{
					Secrets:        v.Secrets,
					Method:         v.Method,
					Path:           v.Path,
					Body:           body,
					Header:         v.Header,
					Now:            v.Now,
					MaxSkewSeconds: v.MaxSkewSeconds,
				})
				switch v.Expect {
				case "ok":
					if verifyErr != nil {
						t.Fatalf("expected ok, got error: %v", verifyErr)
					}
					if result.SecretIndex != v.ExpectedSecretIndex {
						t.Fatalf("expectedSecretIndex %d, got %d", v.ExpectedSecretIndex, result.SecretIndex)
					}
				case "MalformedHeader":
					if !errors.Is(verifyErr, ErrMalformedHeader) {
						t.Fatalf("expected ErrMalformedHeader, got: %v", verifyErr)
					}
				case "StaleTimestamp":
					if !errors.Is(verifyErr, ErrStaleTimestamp) {
						t.Fatalf("expected ErrStaleTimestamp, got: %v", verifyErr)
					}
				case "SignatureMismatch":
					if !errors.Is(verifyErr, ErrSignatureMismatch) {
						t.Fatalf("expected ErrSignatureMismatch, got: %v", verifyErr)
					}
				default:
					t.Fatalf("unknown expect code: %q", v.Expect)
				}

			default:
				t.Fatalf("unknown vector kind: %q", v.Kind)
			}
		})
	}
}

// TestMutationFails confirms that flipping any single byte of a signed
// payload causes verify to fail.
func TestMutationFails(t *testing.T) {
	const secret = "whsec_test_primary_aaaaaaaaaaaaaaaaaaaaaaaaaaa"
	body := []byte(`{"runId":"abc","attempt":1}`)
	res, err := Sign(SignOptions{Secret: secret, Method: "POST", Path: "/api/v1/scheduled/x", Body: body, Timestamp: 1730000000})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	cases := []struct {
		name   string
		mutate func() (method, path string, body []byte, header string)
	}{
		{
			name: "method",
			mutate: func() (string, string, []byte, string) {
				return "GET", "/api/v1/scheduled/x", body, res.Header
			},
		},
		{
			name: "path",
			mutate: func() (string, string, []byte, string) {
				return "POST", "/api/v1/scheduled/y", body, res.Header
			},
		},
		{
			name: "body",
			mutate: func() (string, string, []byte, string) {
				m := append([]byte(nil), body...)
				m[0] ^= 1
				return "POST", "/api/v1/scheduled/x", m, res.Header
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			method, path, b, header := c.mutate()
			if _, err := Verify(VerifyOptions{
				Secrets: []string{secret}, Method: method, Path: path, Body: b, Header: header,
				Now: 1730000000, MaxSkewSeconds: 300,
			}); !errors.Is(err, ErrSignatureMismatch) {
				t.Fatalf("expected ErrSignatureMismatch, got %v", err)
			}
		})
	}
}
