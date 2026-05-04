// Package manifest parses, validates, and normalizes cronix manifests.
//
// Phase 1 of the implementation plan will populate this package. The
// parser must round-trip identically to the TypeScript Zod-based parser
// in @cronix/sdk against the shared conformance vectors at
// packages/spec/manifest-vectors.json.
package manifest
