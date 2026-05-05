package crontab

// Backend-local conventions. Cross-backend rules (schedule name template,
// hash algorithm, drift sentinel) live in internal/policy.
const (
	// DefaultPath is the crontab file cronix reads/writes when
	// Options.Path is empty.
	DefaultPath = "/etc/crontab"

	// lockSuffix is appended to Path to derive the apply-time write
	// mutex when Options.LockPath is empty.
	lockSuffix = ".cronix.lock"

	// ownerMarker is the literal that introduces every cronix-owned
	// comment line. The full owner line format is:
	//
	//   # cronix:owned app=<app> job=<job> hash=<hash> idx=<idx>
	//
	// Lines without this marker are foreign — preserved untouched on
	// every write (D-026).
	ownerMarker = "# cronix:owned"

	// historySyslogIdentifier is the SYSLOG_IDENTIFIER value journald
	// records for `cronix trigger` invocations launched by crond. We
	// match on _COMM= because the trigger binary is named "cronix" and
	// crond doesn't override that.
	historySyslogIdentifier = "_COMM=cronix"
)
