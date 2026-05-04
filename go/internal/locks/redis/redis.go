// Package redis implements the Lock interface against Redis using the
// classic SET NX EX + Lua-fenced refresh/release pattern. This is the v1
// lock backend for jobs whose concurrency_scope is "global" (D-010 / D-023).
package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/awbx/cronix/go/internal/locks"
)

// Backend is the Redis-based Lock implementation.
//
// Acquisition uses `SET key token NX EX ttl` — atomic, idempotent, with a
// TTL so a crashed shim cannot leak the lock past its expiry.
//
// Refresh and Release use Lua scripts so they only act when the caller
// still owns the lock (the stored token still matches). This protects
// against a slow Refresh from a previous holder stepping on a new holder
// who acquired after the previous holder's TTL expired.
type Backend struct {
	client    goredis.UniversalClient
	keyPrefix string
}

// New constructs a Backend using the supplied client. The client's
// lifecycle is the caller's responsibility.
func New(client goredis.UniversalClient, keyPrefix string) *Backend {
	if keyPrefix == "" {
		keyPrefix = "cronix:lock:"
	}
	return &Backend{client: client, keyPrefix: keyPrefix}
}

// Name returns "redis".
func (*Backend) Name() string { return "redis" }

const (
	releaseScript = `if redis.call("GET", KEYS[1]) == ARGV[1] then
return redis.call("DEL", KEYS[1])
else
return 0
end`

	refreshScript = `if redis.call("GET", KEYS[1]) == ARGV[1] then
return redis.call("EXPIRE", KEYS[1], ARGV[2])
else
return 0
end`
)

// Acquire takes the lock for `key` with TTL `ttl`. Fails fast with
// ErrContended unless ctx has a deadline; with a deadline, polls every
// 100ms until acquired or the deadline expires.
func (b *Backend) Acquire(ctx context.Context, key string, ttl time.Duration) (locks.Handle, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		return nil, errors.New("redis lock: ttl must be > 0")
	}
	token, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("redis lock: gen token: %w", err)
	}
	full := b.keyPrefix + key

	if _, deadlineSet := ctx.Deadline(); !deadlineSet {
		got, err := b.client.SetNX(ctx, full, token, ttl).Result()
		if err != nil {
			return nil, fmt.Errorf("redis lock: SETNX %s: %w", full, err)
		}
		if !got {
			return nil, locks.ErrContended
		}
		return &handle{client: b.client, key: full, token: token, ttl: ttl}, nil
	}

	for {
		got, err := b.client.SetNX(ctx, full, token, ttl).Result()
		if err != nil {
			return nil, fmt.Errorf("redis lock: SETNX %s: %w", full, err)
		}
		if got {
			return &handle{client: b.client, key: full, token: token, ttl: ttl}, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, locks.ErrContended
			}
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validateKey(key string) error {
	if key == "" {
		return errors.New("redis lock: key must be non-empty")
	}
	if strings.ContainsAny(key, " \r\n\x00") {
		return fmt.Errorf("redis lock: key contains illegal whitespace/null: %q", key)
	}
	return nil
}

type handle struct {
	mu       sync.Mutex
	client   goredis.UniversalClient
	key      string
	token    string
	ttl      time.Duration
	released bool
}

func (h *handle) Release() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return nil
	}
	h.released = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := h.client.Eval(ctx, releaseScript, []string{h.key}, h.token).Result()
	if err != nil && !errors.Is(err, goredis.Nil) {
		return fmt.Errorf("redis lock: release %s: %w", h.key, err)
	}
	return nil
}

func (h *handle) Refresh(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return errors.New("redis lock: refresh after release")
	}
	res, err := h.client.Eval(ctx, refreshScript, []string{h.key}, h.token, int(h.ttl.Seconds())).Result()
	if err != nil {
		return fmt.Errorf("redis lock: refresh %s: %w", h.key, err)
	}
	if n, ok := res.(int64); ok && n == 0 {
		return errors.New("redis lock: lost lock (token mismatch)")
	}
	return nil
}
