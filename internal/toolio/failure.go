package toolio

// The failure categories the shell defines. Later specs add their own; a
// category is an open string, and docs/cli.md lists them all.
const (
	CategoryUsage     = "usage"
	CategoryAuth      = "auth"
	CategoryForge     = "forge"
	CategoryIO        = "io"
	CategoryCancelled = "cancelled"
	CategoryTimeout   = "timeout"
	CategoryInternal  = "internal"
)

// Failure is the error a command returns to set the envelope's error object.
// A Failure of category usage gives status usage; any other gives failed.
type Failure struct {
	Category  string
	Message   string
	Retryable bool
	FixHint   *FixHint
}

// Error returns the failure's message.
func (f *Failure) Error() string { return f.Message }

// Outcome is what a command returns when it did not fail. Its status is
// done, unverified or needs_human; needs_human carries the question.
type Outcome struct {
	Status     Status
	Summary    string
	Result     any
	NeedsHuman *Question
}

// Question is what a needs_human outcome asks. The shell letters the options
// "A", "B", … in order; no options means free text is expected.
type Question struct {
	Text    string
	Options []string
}
