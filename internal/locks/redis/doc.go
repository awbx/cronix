// Package redis implements the Lock interface against Redis (SET NX EX +
// Lua refresh script) for `concurrency_scope: global`. Phase 4 populates this package.
package redis
