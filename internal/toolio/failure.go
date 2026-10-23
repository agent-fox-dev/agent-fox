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
	// CategoryConfig: a configuration file af reads cannot be used.
	CategoryConfig = "config"
	// CategoryRestore: a tree af changed for a measurement could not be put
	// back, or a leftover of an earlier measurement stands in the way.
	CategoryRestore = "restore"
	// CategoryGit: git failed in a way the command cannot work around.
	CategoryGit = "git"
	// CategorySandbox: the box could not be opened, or its tree could not be
	// synced in or exported out.
	CategorySandbox = "sandbox"
	// CategorySubmission: a submission was refused, and nothing was written.
	CategorySubmission = "submission"
	// CategoryBrain: the brain failed, or panicked, and left no submission.
	CategoryBrain = "brain"
	// CategoryNoResult: the brain stopped and left no valid submission.
	CategoryNoResult = "no_result"
	// CategoryPreflight: a fact about the repository stops the run before it
	// spends anything: a tree that is not clean, no gate to measure by, no origin
	// to push to. It is not an error of the invocation, so it is not usage.
	CategoryPreflight = "preflight"
	// CategoryBudget: the brain used the cost bound or the turn bound and left
	// no submission.
	CategoryBudget = "budget"
	// CategoryService: the agent-fox service could not be reached or gave an answer that
	// af cannot use: a transport error, a 5xx, an answer that is not the document expected.
	CategoryService = "service"
	// CategoryConflict: the service refused the request because of the state of the job or
	// of the workspace, not because of the invocation.
	CategoryConflict = "conflict"
	// CategoryHub: the service could not get an answer from hub.
	CategoryHub = "hub"
	// CategoryJobCancelled: a job that af followed was cancelled on the service. It is not
	// CategoryCancelled, which is a signal sent to the af process.
	CategoryJobCancelled = "job_cancelled"
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
	// Document, when it is not empty, is what stdout gets in place of the
	// envelope. It is for the one command whose caller is a host that reads
	// another format; the status must be done. The envelope is still made,
	// and is still written to --output and to the run directory.
	Document []byte
}

// Question is what a needs_human outcome asks. The shell letters the options
// "A", "B", … in order; no options means free text is expected.
type Question struct {
	Text    string
	Options []string
	// ResumeArgv, when it is not empty, is the argv of needs_human.resume in place of the
	// invocation as it was received.
	ResumeArgv []string
}
