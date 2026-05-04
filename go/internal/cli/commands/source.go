package commands

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/awbx/cronix/go/internal/auth"
	"github.com/awbx/cronix/go/internal/headers"
	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/trigger"
)

const manifestPath = "/.well-known/cron-manifest"

// loadManifestBytes fetches the manifest body for a `--manifest` arg.
// Supports `file://path`, `file:path`, an absolute path (starts with `/`),
// `https://url`, and `http://localhost`/`http://127.0.0.1` for dev.
//
// HTTPS fetches sign the GET with the first resolved secret_ref.
func loadManifestBytes(ctx context.Context, source string, secretRefs []string) ([]byte, error) {
	switch {
	case strings.HasPrefix(source, "file://"):
		return os.ReadFile(strings.TrimPrefix(source, "file://"))
	case strings.HasPrefix(source, "file:"):
		return os.ReadFile(strings.TrimPrefix(source, "file:"))
	case strings.HasPrefix(source, "/"), strings.HasPrefix(source, "./"):
		return os.ReadFile(source)
	case strings.HasPrefix(source, "https://"),
		strings.HasPrefix(source, "http://localhost"),
		strings.HasPrefix(source, "http://127.0.0.1"):
		return fetchHTTP(ctx, source, secretRefs)
	}
	return nil, fmt.Errorf("manifest source must be file://, an absolute path, https://, or http://localhost — got %q", source)
}

func fetchHTTP(ctx context.Context, url string, secretRefs []string) ([]byte, error) {
	if len(secretRefs) == 0 {
		return nil, fmt.Errorf("manifest fetch %s requires at least one secret_ref", url)
	}
	secrets, err := trigger.ResolveSecrets(secretRefs)
	if err != nil {
		return nil, fmt.Errorf("resolve secrets: %w", err)
	}
	signed, err := auth.Sign(auth.SignOptions{
		Secret:    secrets[0],
		Method:    "GET",
		Path:      manifestPath,
		Body:      nil,
		Timestamp: time.Now().Unix(),
	})
	if err != nil {
		return nil, fmt.Errorf("sign manifest fetch: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(headers.Signature, signed.Header)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: status %d body=%q", url, resp.StatusCode, body)
	}
	return body, nil
}

// loadAndNormalize reads the manifest bytes and produces a parsed +
// normalized manifest. The bytes are JSON; YAML manifests are out of
// scope for v1.
func loadAndNormalize(ctx context.Context, source string, secretRefs []string) (*manifest.NormalizedManifest, error) {
	raw, err := loadManifestBytes(ctx, source, secretRefs)
	if err != nil {
		return nil, err
	}
	parsed, err := manifest.Parse(raw)
	if err != nil {
		return nil, err
	}
	return manifest.ApplyDefaults(parsed), nil
}
