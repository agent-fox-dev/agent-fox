// Package afclient is the client of the agent-fox service: the server a command talks to,
// the credential it sends, and the requests it makes. It returns plain and typed errors and
// never exits; the commands of cmd/af map them to the envelope.
package afclient

import (
	"net"
	"net/url"
	"strings"
)

// The places a server URL comes from.
const (
	SourceFlag = "--server"
	SourceEnv  = "AF_URL"
)

// Server is the agent-fox service a command talks to.
type Server struct {
	// URL is the base URL without a trailing slash; the API is at URL + "/api/v1/…".
	URL string
	// Source says what gave the URL: SourceFlag or SourceEnv.
	Source string
	// FromFlag is true when --server named the server, so that the argv a command suggests
	// carries the flag.
	FromFlag bool

	host *url.URL
}

// ServerError is a server URL that cannot be used, or none at all.
type ServerError struct {
	// Source is what gave the URL; empty when Missing.
	Source string
	// Missing is true when neither --server nor AF_URL named a server.
	Missing bool
	// Rule is the rule the URL breaks.
	Rule string
}

func (e *ServerError) Error() string {
	if e.Missing {
		return "no server is named: pass --server or set AF_URL to the URL of the agent-fox service"
	}
	return "the server URL given by " + e.Source + " " + e.Rule
}

// ParseServer reads the URL of the server: flagValue when flagGiven, else envValue, and an
// empty envValue counts as unset. A flag that was given empty is a refusal, not a fall back
// to the variable, so that a credential never goes to a server the person did not name. The
// URL is an absolute http or https URL with a host and an optional path prefix, and has no
// query, no fragment and no user info; one trailing "/" is dropped. An error is a
// *ServerError, and never holds the URL, which may hold a password.
func ParseServer(flagValue string, flagGiven bool, envValue string) (Server, error) {
	raw, source := envValue, SourceEnv
	switch {
	case flagGiven:
		raw, source = flagValue, SourceFlag
	case envValue == "":
		return Server{}, &ServerError{Missing: true}
	}
	refuse := func(rule string) (Server, error) { return Server{}, &ServerError{Source: source, Rule: rule} }

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return refuse("must be an absolute http or https URL")
	}
	switch {
	case u.User != nil:
		return refuse("must not carry user info (user:password@host)")
	case u.Hostname() == "":
		return refuse("must name a host")
	case u.RawQuery != "" || u.ForceQuery:
		return refuse("must not carry a query")
	case u.Fragment != "" || strings.Contains(raw, "#"):
		return refuse("must not carry a fragment")
	}
	base := u.Scheme + "://" + u.Host + strings.TrimSuffix(u.EscapedPath(), "/")
	return Server{URL: base, Source: source, FromFlag: flagGiven, host: u}, nil
}

// Insecure reports whether the credential would cross the link in clear: the URL is http and
// its host is not loopback (localhost, 127.0.0.0/8 or ::1).
func (s Server) Insecure() bool {
	if s.host == nil || s.host.Scheme != "http" {
		return false
	}
	host := s.host.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}
