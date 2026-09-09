package agentspec

// AgentSpecError is the base error interface for all agentspec error types.
// All concrete error types in this package satisfy this interface so that
// callers can match any agentspec error with errors.As(err, &target) and
// inspect the category via target.Category().
type AgentSpecError interface {
	error
	Category() string
}

// ConfigError is returned for configuration-related failures such as
// invalid TOML syntax, missing required fields, or symlinked config files.
type ConfigError struct {
	Msg   string
	Cause error
}

func (e *ConfigError) Error() string    { return e.Msg }
func (e *ConfigError) Category() string { return "config" }
func (e *ConfigError) Unwrap() error    { return e.Cause }

// CampaignError is returned for campaign directory operation failures
// such as duplicate paths, missing campaign.yaml, or invalid spec names.
type CampaignError struct {
	Msg   string
	Cause error
}

func (e *CampaignError) Error() string    { return e.Msg }
func (e *CampaignError) Category() string { return "campaign" }
func (e *CampaignError) Unwrap() error    { return e.Cause }

// SessionError is returned for illegal state transitions or invalid
// session state operations.
type SessionError struct {
	Msg   string
	Cause error
}

func (e *SessionError) Error() string    { return e.Msg }
func (e *SessionError) Category() string { return "state" }
func (e *SessionError) Unwrap() error    { return e.Cause }

// AgentError is the richest error type, carrying structured details about
// an agent operation failure.
type AgentError struct {
	Detail        string
	ErrorCategory string
	Retryable     bool
	HTTPStatus    *int
	Cause         error
}

func (e *AgentError) Error() string    { return e.Detail }
func (e *AgentError) Category() string { return e.ErrorCategory }
func (e *AgentError) Unwrap() error    { return e.Cause }
