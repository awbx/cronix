package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/awbx/cronix/go/internal/trigger"
)

// resolver extends the local env/file/raw resolver with `ssm:` and
// `secretsmanager:` schemes. AWS clients are lazily constructed so a
// Lambda that only uses env/raw secrets pays no init cost for SDK
// clients it never touches.
type resolver struct {
	ctx     context.Context
	mu      sync.Mutex
	ssm     *ssm.Client
	sm      *secretsmanager.Client
	loadErr error
	loaded  bool
}

func newResolver(ctx context.Context) *resolver {
	return &resolver{ctx: ctx}
}

// Resolve walks `refs`, dispatching to the local resolver for env/file/raw
// and to AWS APIs for ssm/secretsmanager. Order is preserved so the
// shim's first-secret-signs / verifier-side secret_index reporting stays
// stable across deploys.
func (r *resolver) Resolve(refs []string) ([]string, error) {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		scheme, rest, ok := strings.Cut(ref, ":")
		if !ok {
			return nil, fmt.Errorf("trigger: secret_ref must be `<scheme>:<value>`, got %q", ref)
		}
		switch scheme {
		case "env", "file", "raw":
			vals, err := trigger.ResolveSecrets([]string{ref})
			if err != nil {
				return nil, err
			}
			out = append(out, vals...)
		case "ssm":
			v, err := r.fetchSSM(rest)
			if err != nil {
				return nil, fmt.Errorf("trigger: ssm:%s: %w", rest, err)
			}
			if v != "" {
				out = append(out, v)
			}
		case "secretsmanager", "sm":
			v, err := r.fetchSecretsManager(rest)
			if err != nil {
				return nil, fmt.Errorf("trigger: secretsmanager:%s: %w", rest, err)
			}
			if v != "" {
				out = append(out, v)
			}
		default:
			return nil, fmt.Errorf("trigger: unknown secret_ref scheme %q (want env|file|raw|ssm|secretsmanager)", scheme)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("trigger: no resolvable secrets")
	}
	return out, nil
}

func (r *resolver) loadAWS() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return r.loadErr
	}
	r.loaded = true
	cfg, err := config.LoadDefaultConfig(r.ctx)
	if err != nil {
		r.loadErr = fmt.Errorf("load AWS config: %w", err)
		return r.loadErr
	}
	r.ssm = ssm.NewFromConfig(cfg)
	r.sm = secretsmanager.NewFromConfig(cfg)
	return nil
}

func (r *resolver) fetchSSM(name string) (string, error) {
	if err := r.loadAWS(); err != nil {
		return "", err
	}
	out, err := r.ssm.GetParameter(r.ctx, &ssm.GetParameterInput{
		Name:           ptr(name),
		WithDecryption: ptr(true),
	})
	if err != nil {
		return "", err
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", nil
	}
	return *out.Parameter.Value, nil
}

func (r *resolver) fetchSecretsManager(id string) (string, error) {
	if err := r.loadAWS(); err != nil {
		return "", err
	}
	out, err := r.sm.GetSecretValue(r.ctx, &secretsmanager.GetSecretValueInput{
		SecretId: ptr(id),
	})
	if err != nil {
		return "", err
	}
	if out.SecretString != nil {
		return *out.SecretString, nil
	}
	if len(out.SecretBinary) > 0 {
		return string(out.SecretBinary), nil
	}
	return "", nil
}

func ptr[T any](v T) *T { return &v }
