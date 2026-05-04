package trigger

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ResolveSecrets turns secret_refs (e.g. "env:CRON_SECRET_V2",
// "file:/run/secrets/cron", "raw:literal") into the raw bytes used by
// HMAC. Order is preserved so verifier-side secret_index reporting is
// stable.
//
// Empty resolutions are skipped (with a logged warning by the caller)
// rather than treated as fatal — operator deploys often have one-off
// missing secrets during rotation, and an empty list is what triggers
// the "no acceptable secret" error path which is more diagnostic.
func ResolveSecrets(refs []string) ([]string, error) {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		v, err := resolveOne(ref)
		if err != nil {
			return nil, err
		}
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("trigger: no resolvable secrets")
	}
	return out, nil
}

func resolveOne(ref string) (string, error) {
	scheme, rest, ok := strings.Cut(ref, ":")
	if !ok {
		return "", fmt.Errorf("trigger: secret_ref must be `<scheme>:<value>`, got %q", ref)
	}
	switch scheme {
	case "env":
		return os.Getenv(rest), nil
	case "file":
		raw, err := os.ReadFile(rest) //#nosec G304 — operator-managed path
		if err != nil {
			return "", fmt.Errorf("trigger: read secret file %s: %w", rest, err)
		}
		return string(bytes.TrimSpace(raw)), nil
	case "raw":
		return rest, nil
	default:
		return "", fmt.Errorf("trigger: unknown secret_ref scheme %q (want env|file|raw)", scheme)
	}
}
