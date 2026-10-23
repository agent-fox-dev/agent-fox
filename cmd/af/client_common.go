package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/agent-fox-dev/agentfox/internal/afclient"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The warnings the commands of the service client raise.
const (
	// warnServerInsecure: the server is plain http and not on this machine, so the credential crosses the link in clear.
	warnServerInsecure = "server_insecure"
	// warnCredentialFilePermissions: the credential file has a group or other permission bit set.
	warnCredentialFilePermissions = "credential_file_permissions"
)

// loginSuggestion is how a person stores a credential, as the fix hint of a missing or refused one gives it.
const loginSuggestion = "af login --token-file FILE"

// httpTransport is the transport of the requests the client commands make; nil is the default
// transport. The tests replace it.
var httpTransport http.RoundTripper

// schemaFiles are the JSON Schemas of the results of the client commands, embedded in the
// binary so that af --schema prints them.
//
//go:embed schemas/*.json
var schemaFiles embed.FS

// resultSchema is the embedded schema schemas/<name>.json. A missing file is a mistake in
// the build, which a test finds, and not a condition to handle.
func resultSchema(name string) json.RawMessage {
	data, err := schemaFiles.ReadFile("schemas/" + name + ".json")
	if err != nil {
		panic(fmt.Sprintf("the result schema %q is not embedded: %v", name, err))
	}
	return data
}

// serverFlag is --server, which remembers that it was given: an empty value that was given is
// a refusal, not a fall back to AF_URL.
type serverFlag struct {
	value string
	given bool
}

func (f *serverFlag) String() string { return f.value }

func (f *serverFlag) Set(v string) error {
	f.value, f.given = v, true
	return nil
}

// bind registers --server on fs, and forgets the value of an earlier invocation.
func (f *serverFlag) bind(fs *flag.FlagSet) {
	*f = serverFlag{}
	fs.Var(f, "server", "the `URL` of the agent-fox service; AF_URL when the flag is absent")
}

// connect runs the local checks of a service command that come after its flags and its
// input, in this order: the server, then the credential. It returns the client, or the failure
// to end the command with; nothing has been sent. local says that the command has a local mode,
// which the message of a missing server then names too.
func connect(r *toolio.Run, app *toolio.App, server *serverFlag, local bool) (*afclient.Client, error) {
	srv, err := afclient.ParseServer(server.value, server.given, os.Getenv(afclient.SourceEnv))
	if err != nil {
		return nil, serverFailure(err, local)
	}
	warnInsecure(r, srv)
	cred, err := afclient.LoadCredential(nil)
	if err != nil {
		return nil, credentialFailure(err)
	}
	if cred.Loose {
		r.Warn(warnCredentialFilePermissions, fmt.Sprintf("the credential file %s has a group or other permission bit set; restrict it with chmod 600", cred.Path))
	}
	return newClient(app, srv, cred.Token), nil
}

// warnInsecure raises the warning for a server that is plain http and not on this machine.
func warnInsecure(r *toolio.Run, srv afclient.Server) {
	if srv.Insecure() {
		r.Warn(warnServerInsecure, fmt.Sprintf("the server %s is plain http and is not on this machine, so the hub credential crosses the link in clear; use an https URL", srv.URL))
	}
}

// newClient is the client of srv that sends token. Its User-Agent is af/ and the version of the build.
func newClient(app *toolio.App, srv afclient.Server, token string) *afclient.Client {
	ver, _ := app.Identity()
	client := afclient.New(srv, token, "af/"+ver, httpTransport)
	client.Clock = clientClock
	return client
}

// noServerFailure is the usage failure for a service command that names no server. local says
// that the command has a local mode (triage and fix), which the message names.
func noServerFailure(local bool) *toolio.Failure {
	msg := "no server is named: pass --server URL or set AF_URL to the URL of the agent-fox service"
	if local {
		msg += ", or pass --local to run on this machine"
	}
	return &toolio.Failure{Category: toolio.CategoryUsage, Message: msg, FixHint: &toolio.FixHint{Flag: "--server", Env: afclient.SourceEnv}}
}

// serverFailure is the failure for a server URL that cannot be used.
func serverFailure(err error, local bool) error {
	var se *afclient.ServerError
	if !errors.As(err, &se) {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: err.Error()}
	}
	if se.Missing {
		return noServerFailure(local)
	}
	hint := &toolio.FixHint{Flag: afclient.SourceFlag}
	if se.Source == afclient.SourceEnv {
		hint = &toolio.FixHint{Env: afclient.SourceEnv}
	}
	return &toolio.Failure{Category: toolio.CategoryUsage, Message: se.Error(), FixHint: hint}
}

// credentialFailure is the failure for a credential that cannot be used. The message names the file and never the token.
func credentialFailure(err error) error {
	var ce *afclient.CredentialError
	if !errors.As(err, &ce) {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: err.Error()}
	}
	f := &toolio.Failure{Message: ce.Message}
	switch ce.Kind {
	case afclient.CredentialAuth:
		f.Category, f.FixHint = toolio.CategoryAuth, &toolio.FixHint{Suggest: loginSuggestion}
	case afclient.CredentialConfig:
		f.Category = toolio.CategoryConfig
	default:
		f.Category = toolio.CategoryIO
	}
	return f
}

// secretFailure is the failure for a secret (a credential or a token) that cannot be read or used.
func secretFailure(err error) error {
	var se *afclient.SecretError
	if !errors.As(err, &se) {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: err.Error()}
	}
	if se.Kind == afclient.SecretContent {
		return &toolio.Failure{Category: toolio.CategoryUsage, Message: se.Message}
	}
	return &toolio.Failure{Category: toolio.CategoryIO, Message: se.Message}
}

// requestFailure is the failure for a request that did not end in a 2xx answer: the category,
// message, retryable flag and fix hint of the answer. A request that the shell stopped, by a
// signal or by --timeout, is left to the shell.
func requestFailure(err error) error {
	var ae *afclient.Error
	if !errors.As(err, &ae) {
		return err
	}
	f := &toolio.Failure{Category: ae.Category, Message: ae.Message, Retryable: ae.Retryable}
	if h := ae.Hint; h != nil {
		f.FixHint = &toolio.FixHint{Flag: h.Flag, Env: h.Env, Suggest: h.Suggest}
	}
	return f
}

// clientClock is the clock of the requests the client commands make; nil is the system's. The
// tests replace it.
var clientClock afclient.Clock

// credentialFS is the file system af login writes the credential file to; nil is the operating
// system's. The tests replace it.
var credentialFS afclient.FS
