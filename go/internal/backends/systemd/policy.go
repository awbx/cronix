package systemd

// Backend-local conventions. Cross-backend rules (schedule name template,
// hash algorithm, drift sentinel) live in internal/policy.
//
// Anything in this file is "the systemd contract" — change it and every
// existing deployment surfaces drift on the next `cronix apply`.
const (
	// DefaultUnitDir is where cronix writes timer + service unit files
	// when Options.UnitDir is empty. Operators using a non-default
	// install (eg. user-scoped units under ~/.config/systemd/user/) must
	// set Options.UnitDir explicitly.
	DefaultUnitDir = "/etc/systemd/system"

	// systemdRunDir is the well-known marker that systemd is the active
	// init system. Ensure() refuses to run if it is missing.
	systemdRunDir = "/run/systemd/system"

	// timeoutHeadroomSeconds is added to the job's policy timeout when
	// rendering RuntimeMaxSec=. The shim enforces the actual timeout via
	// context.WithTimeout; RuntimeMaxSec is a defense-in-depth ceiling
	// systemd applies if the shim hangs past its own timeout. Headroom
	// keeps systemd from killing the shim mid-cleanup on a borderline
	// timeout.
	timeoutHeadroomSeconds = 30

	// X-Cronix-* annotation keys we write into every owned unit file.
	// systemd ignores X- prefixed fields, so they double as our
	// ownership marker — a unit without these is not cronix-owned.
	annotationApp        = "X-Cronix-App"
	annotationJob        = "X-Cronix-Job"
	annotationIndex      = "X-Cronix-Index"
	annotationHash       = "X-Cronix-Hash"
	annotationOnCalendar = "X-Cronix-OnCalendar"
)
