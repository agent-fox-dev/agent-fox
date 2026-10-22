package recovery

import "encoding/json"

// Plan is what a landing needs to be finished, and what it reports, once the commit is made and before
// the first push.
type Plan struct {
	Branch  string `json:"branch"`
	Commit  string `json:"commit"`
	Outcome string `json:"outcome"`
	Status  string `json:"status"`
	Verdict string `json:"verdict"`
	// Closes says the change closes the issue.
	Closes bool   `json:"closes"`
	Title  string `json:"pull_request_title"`
	Body   string `json:"pull_request_body"`
	Draft  bool   `json:"pull_request_draft"`
	// Warnings are the warnings the run had raised by then, and Result the result of the run as it
	// stood, as the shell encodes it.
	Warnings []Warning       `json:"warnings"`
	Result   json.RawMessage `json:"result,omitempty"`
}

// Warning is a warning of the envelope.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
