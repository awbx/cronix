// Package auth implements HMAC-SHA256 signing and verification for
// cronix manifests and triggers (Stripe-shaped: t=<unix>,v1=<hex>).
//
// Phase 2 populates this package. Verification must be constant-time
// and pass every case in packages/spec/auth-vectors.json.
package auth
