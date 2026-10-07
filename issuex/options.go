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
	// Dir is the working tree whose origin remote decides the forge when
	// nothing else does. Empty means the process's working directory.
	Dir string
	// withholdEnvToken is set by forge detection when the API base it chose
	// is not the host the environment's token is configured for: the client
	// then sends no token rather than another host's.
	withholdEnvToken bool
}
