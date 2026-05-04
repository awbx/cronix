// Package locks defines the Lock interface used by the trigger shim
// to enforce per-job concurrency policies.
//
// Phase 4 ships the interface plus a flock-based local implementation
// (host scope) and a Redis-based distributed implementation (global scope).
package locks
