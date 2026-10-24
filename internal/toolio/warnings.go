package toolio

import "regexp"

// The warnings the shell raises about what it wrote, besides those of the
// input (WarnInputTruncated, WarnCommentsUnreadable).
const (
	// WarnEnvelopeUnwritable: <run_dir>/envelope.json could not be written.
	WarnEnvelopeUnwritable = "envelope_unwritable"
	// WarnEventsUnwritable: an event could not be written to the run
	// directory. It is raised once.
	WarnEventsUnwritable = "events_unwritable"
	// WarnInvalidCode: a command raised a warning whose code does not match
	// the pattern of a code. The message begins with that code.
	WarnInvalidCode = "invalid_warning_code"
)

// WarningCodes lists the warning codes the shell itself raises. Later specs
// add their own, and docs/cli.md lists them all.
var WarningCodes = []string{
	WarnInputTruncated,
	WarnCommentsUnreadable,
	WarnEnvelopeUnwritable,
	WarnEventsUnwritable,
	WarnInvalidCode,
}

// warningCodeFormat is what every warning code looks like.
var warningCodeFormat = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
