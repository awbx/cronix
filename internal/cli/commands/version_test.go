package commands

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	root := NewRootCmd()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "cronix") {
		t.Errorf("version output missing program name: %q", out)
	}
	if !strings.Contains(out, "commit:") {
		t.Errorf("version output missing commit field: %q", out)
	}
}
