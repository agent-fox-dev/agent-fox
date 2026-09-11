package issuex

import (
	"net/http"
)

// Options configures client creation for Git forge operations.
type Options struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
	UserAgent  string
	NoOp       bool
	Repo       Repo
	RemoteURL  string
}
