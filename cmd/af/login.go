package main

import (
	"errors"
	"flag"
	"net/http"
	"os"

	"github.com/agent-fox-dev/agentfox/internal/afclient"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// loginResult is the result of af login: where the credential is, and the service that took it.
// The credential is never in it.
type loginResult struct {
	CredentialFile string `json:"credential_file"`
	Server         string `json:"server"`
}

// registerLogin registers af login.
func registerLogin(app *toolio.App) {
	var tokenFile string
	var server serverFlag
	app.Register(toolio.Command{
		Path:    "login",
		Summary: "Store the hub credential that af sends to the agent-fox service",
		Usage:   "af login --token-file FILE|- [--server URL]",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&tokenFile, "token-file", "", "the `file` that holds the credential, an API key or a personal access token; - reads stdin")
			server.bind(fs)
		},
		MaxArgs:      toolio.NoArgs,
		RawStdin:     true,
		Timeout:      true,
		ResultSchema: resultSchema("login"),
		Run:          func(r *toolio.Run) (toolio.Outcome, error) { return login(r, app, tokenFile, &server) },
	})
}

// login stores a credential in ~/.af/config.toml. Its steps run in this order, and a step that
// fails ends the command with nothing written: the credential is read, the server is found, the
// credential file that is there is checked, the credential is checked with the service, and the
// file is written.
func login(r *toolio.Run, app *toolio.App, tokenFile string, server *serverFlag) (toolio.Outcome, error) {
	if tokenFile == "" {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--token-file is required: name the file that holds the credential, or - to read it from stdin", FixHint: &toolio.FixHint{Flag: "--token-file"}}
	}
	credential, err := afclient.ReadSecretFile("--token-file", tokenFile, r.Stdin)
	if err != nil {
		return toolio.Outcome{}, secretFailure(err)
	}
	srv, err := afclient.ParseServer(server.value, server.given, os.Getenv(afclient.SourceEnv))
	if err != nil {
		return toolio.Outcome{}, serverFailure(err, false)
	}
	r.Redact(credential)
	warnInsecure(r, srv)
	existing, err := afclient.ReadExisting(nil)
	if err != nil {
		return toolio.Outcome{}, credentialFailure(err)
	}

	if _, err := newClient(app, srv, credential).Do(r.Ctx, http.MethodGet, "/workspaces", nil, nil); err != nil {
		return toolio.Outcome{}, checkFailure(err)
	}

	fsys := credentialFS
	if fsys == nil {
		fsys = afclient.OSFS{}
	}
	if err := afclient.StoreCredential(fsys, existing, credential); err != nil {
		return toolio.Outcome{}, credentialFailure(err)
	}
	return toolio.Outcome{
		Status:  toolio.StatusDone,
		Summary: "stored the credential in " + existing.Path + ", which " + srv.URL + " accepts",
		Result:  loginResult{CredentialFile: existing.Path, Server: srv.URL},
	}, nil
}

// checkFailure is the failure for a check of the credential that did not end in a 2xx answer.
// Only a 401 says that the service refused the credential; every other ending says that it could
// not be checked, so that a valid credential is never reported as refused.
func checkFailure(err error) error {
	var ae *afclient.Error
	if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
		return &toolio.Failure{
			Category: toolio.CategoryAuth,
			Message:  "the service refused this credential, so nothing was stored: " + ae.Message,
			FixHint:  &toolio.FixHint{Flag: "--token-file"},
		}
	}
	failure := requestFailure(err)
	var f *toolio.Failure
	if errors.As(failure, &f) {
		f.Message = "the credential could not be checked, so nothing was stored: " + f.Message
	}
	return failure
}
