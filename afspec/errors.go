package afspec

import "fmt"

// SpecError is the base error type for all afspec library errors.
// All specific error types wrap SpecError such that errors.As(err, &SpecError{})
// returns true for any afspec error.
type SpecError struct {
	Msg string
}

func (e *SpecError) Error() string {
	return e.Msg
}

// LoadError is returned when a spec directory cannot be loaded due to
// missing required files or malformed content.
type LoadError struct {
	Msg  string
	File string
	Err  *SpecError
}

func (e *LoadError) Error() string {
	if e.File != "" {
		return fmt.Sprintf("load error: %s: %s", e.File, e.Msg)
	}
	return fmt.Sprintf("load error: %s", e.Msg)
}

func (e *LoadError) Unwrap() error {
	return e.Err
}

// SaveError is returned when a spec cannot be written to disk.
type SaveError struct {
	Msg string
	Err *SpecError
}

func (e *SaveError) Error() string {
	return fmt.Sprintf("save error: %s", e.Msg)
}

func (e *SaveError) Unwrap() error {
	return e.Err
}

// LifecycleError is returned when an invalid spec or subtask state
// transition is attempted.
type LifecycleError struct {
	Msg string
	Err *SpecError
}

func (e *LifecycleError) Error() string {
	return fmt.Sprintf("lifecycle error: %s", e.Msg)
}

func (e *LifecycleError) Unwrap() error {
	return e.Err
}

// IntentError is returned when ComputeIntentHash cannot find the
// ## Intent section in a PRD body.
type IntentError struct {
	Msg string
	Err *SpecError
}

func (e *IntentError) Error() string {
	return fmt.Sprintf("intent error: %s", e.Msg)
}

func (e *IntentError) Unwrap() error {
	return e.Err
}

// BootstrapError is returned when BootstrapSpec.Finalize fails due to
// missing artifacts or validation errors.
type BootstrapError struct {
	Msg string
	Err *SpecError
}

func (e *BootstrapError) Error() string {
	return fmt.Sprintf("bootstrap error: %s", e.Msg)
}

func (e *BootstrapError) Unwrap() error {
	return e.Err
}
