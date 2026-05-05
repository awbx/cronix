package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_valid(t *testing.T) {
	path := writeTemp(t, `
version: 1
backends:
  - name: laptop
    type: crontab
    crontab_path: /var/at/tabs/me
    trigger_bin: /usr/local/bin/cronix
  - name: prod-cluster
    type: kubernetes
    namespace: scheduled
    in_cluster: true
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Backends) != 2 {
		t.Fatalf("want 2 backends, got %d", len(c.Backends))
	}
	if c.Backends[0].Name != "laptop" || c.Backends[0].Type != "crontab" {
		t.Errorf("unexpected first entry: %+v", c.Backends[0])
	}
	if c.Backends[1].Namespace != "scheduled" || !c.Backends[1].InCluster {
		t.Errorf("unexpected second entry: %+v", c.Backends[1])
	}
}

func TestLoad_invalid(t *testing.T) {
	cases := map[string]struct {
		yaml    string
		wantSub string
	}{
		"bad version": {
			yaml:    "version: 99\nbackends: []\n",
			wantSub: "version must be 1",
		},
		"missing name": {
			yaml:    "version: 1\nbackends:\n  - type: crontab\n    crontab_path: /x\n",
			wantSub: "name is required",
		},
		"duplicate name": {
			yaml: `version: 1
backends:
  - name: same
    type: crontab
    crontab_path: /a
  - name: same
    type: crontab
    crontab_path: /b
`,
			wantSub: `duplicate backend name "same"`,
		},
		"missing type": {
			yaml:    "version: 1\nbackends:\n  - name: foo\n",
			wantSub: "type is required",
		},
		"unknown type": {
			yaml:    "version: 1\nbackends:\n  - name: foo\n    type: bogus\n",
			wantSub: `unknown type "bogus"`,
		},
		"crontab without path": {
			yaml:    "version: 1\nbackends:\n  - name: foo\n    type: crontab\n",
			wantSub: "crontab_path is required",
		},
		"systemd without unit_dir": {
			yaml:    "version: 1\nbackends:\n  - name: foo\n    type: systemd-timer\n",
			wantSub: "unit_dir is required",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeTemp(t, tc.yaml)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestLoad_missingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolvedPath_returnsEmptyWhenNothingExists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := ResolvedPath(); got != "" {
		t.Errorf("ResolvedPath() = %q, want empty (no real config in fresh HOME)", got)
	}
}

func TestDefaultPaths_includesUserAndSystem(t *testing.T) {
	t.Setenv("HOME", "/fake/home")
	got := DefaultPaths()
	if len(got) < 2 {
		t.Fatalf("want at least 2 paths, got %v", got)
	}
	if !strings.HasSuffix(got[0], "/.cronix/config.yaml") {
		t.Errorf("first path = %q, want HOME/.cronix/config.yaml", got[0])
	}
	if got[len(got)-1] != "/etc/cronix/config.yaml" {
		t.Errorf("last path = %q, want /etc/cronix/config.yaml", got[len(got)-1])
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
