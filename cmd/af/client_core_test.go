package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// recordedRequest is a request a fake service saw.
type recordedRequest struct {
	Method, Path, RequestURI string
	Header                   http.Header
	Body                     []byte
}

// fakeService is a recording stand-in for the agent-fox service. Its handler answers 200
// with an empty list of workspaces unless a test gives it another.
type fakeService struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

// newFakeService starts a fake service that answers every request with handler, or with an
// empty list of workspaces when handler is nil.
func newFakeService(t *testing.T, handler http.HandlerFunc) *fakeService {
	t.Helper()
	s := &fakeService{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, recordedRequest{r.Method, r.URL.Path, r.RequestURI, r.Header.Clone(), body})
		s.mu.Unlock()
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"workspaces":[]}`)
	}))
	t.Cleanup(s.Close)
	return s
}

// Requests are the requests the service has seen, in order.
func (s *fakeService) Requests() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// afResult is what an invocation of the binary's shell wrote.
type afResult struct {
	code           int
	env            map[string]any
	stdout, stderr string
}

// afRun runs the shell of the binary with args and stdin.
func afRun(t *testing.T, stdin string, args ...string) afResult {
	t.Helper()
	var out, errb bytes.Buffer
	code := newApp().Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	res := afResult{code: code, stdout: out.String(), stderr: errb.String()}
	if out.Len() > 0 {
		res.env = oneDocument(t, res.stdout)
	}
	return res
}

// message is the error message of the envelope, or "".
func (r afResult) message() string {
	s, _ := field(r.env, "error", "message").(string)
	return s
}

// category is the error category of the envelope, or "".
func (r afResult) category() string {
	s, _ := field(r.env, "error", "category").(string)
	return s
}

// output is everything the invocation wrote.
func (r afResult) output() string { return r.stdout + r.stderr }

// clientHome makes a home directory for the invocation, with the credential file when
// content is given, and returns the home and the path of the credential file.
func clientHome(t *testing.T, content *string) (home, path string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	path = filepath.Join(home, ".af", "config.toml")
	if content != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(*content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, path
}

// str returns a pointer to s.
func str(s string) *string { return &s }

// roundTripFunc is a transport made of a function.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// useTransport makes the client commands send their requests through rt for the test.
func useTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	old := httpTransport
	httpTransport = rt
	t.Cleanup(func() { httpTransport = old })
}

// TestServerFlagWinsOverAFURLAndAnEmptyAFURLCountsAsUnset_TS_16_17 verifies TS-16-17:
// --server wins over AF_URL, AF_URL is used without the flag, and an empty AF_URL with no
// flag is the no-server usage error.
// Verifies: 16-REQ-3.1
func TestServerFlagWinsOverAFURLAndAnEmptyAFURLCountsAsUnset_TS_16_17(t *testing.T) {
	a, b := newFakeService(t, nil), newFakeService(t, nil)
	clientHome(t, str("token = \"T\"\n"))

	t.Setenv("AF_URL", b.URL)
	if res := afRun(t, "", "workspace", "list", "--server", a.URL); res.code != 0 {
		t.Fatalf("with --server and AF_URL: exit %d, %s", res.code, res.stdout)
	}
	if len(a.Requests()) == 0 || len(b.Requests()) != 0 {
		t.Errorf("with both, A saw %d requests and B %d, want requests to A only", len(a.Requests()), len(b.Requests()))
	}

	if res := afRun(t, "", "workspace", "list"); res.code != 0 {
		t.Fatalf("with AF_URL only: exit %d, %s", res.code, res.stdout)
	}
	if len(b.Requests()) == 0 {
		t.Errorf("with AF_URL only, B saw no request")
	}

	seen := len(a.Requests()) + len(b.Requests())
	t.Setenv("AF_URL", "")
	res := afRun(t, "", "workspace", "list")
	if res.code != 2 || !strings.Contains(res.message(), "--server") || !strings.Contains(res.message(), "AF_URL") {
		t.Errorf("an empty AF_URL and no flag: exit %d, message %q, want 2 naming --server and AF_URL", res.code, res.message())
	}
	if after := len(a.Requests()) + len(b.Requests()); after != seen {
		t.Errorf("the no-server invocation sent %d requests", after-seen)
	}
}

// TestAServerWithAPathPrefixIsAcceptedAndOneTrailingSlashIsDropped_TS_16_18 verifies TS-16-18:
// the API is at <server>/api/v1/ under the path prefix, with no double slash.
// Verifies: 16-REQ-3.2
func TestAServerWithAPathPrefixIsAcceptedAndOneTrailingSlashIsDropped_TS_16_18(t *testing.T) {
	clientHome(t, str("token = \"T\"\n"))
	t.Setenv("AF_URL", "")
	for _, suffix := range []string{"/prefix/", "/prefix"} {
		svc := newFakeService(t, nil)
		if res := afRun(t, "", "workspace", "list", "--server", svc.URL+suffix); res.code != 0 {
			t.Fatalf("--server %s: exit %d, %s", suffix, res.code, res.stdout)
		}
		reqs := svc.Requests()
		if len(reqs) != 1 || reqs[0].RequestURI != "/prefix/api/v1/workspaces" {
			t.Errorf("--server %s: requests %+v, want one for /prefix/api/v1/workspaces", suffix, reqs)
		}
	}
}

// TestAMalformedServerURLIsAUsageErrorNamingItsSourceAndTheRuleAndSendsNothing_TS_16_19
// verifies TS-16-19: each of the URLs is refused once by --server and once by AF_URL, with
// a message that names the source and the rule, never the password, and no request.
// Verifies: 16-REQ-3.3
func TestAMalformedServerURLIsAUsageErrorNamingItsSourceAndTheRuleAndSendsNothing_TS_16_19(t *testing.T) {
	svc := newFakeService(t, nil)
	host := strings.TrimPrefix(svc.URL, "http://")
	const password = "hunter2pw"
	for _, c := range []struct{ url, rule string }{
		{"localhost:80", "http or https"},
		{"ftp://" + host, "http or https"},
		{"http://", "host"},
		{svc.URL + "/?q=1", "query"},
		{svc.URL + "/#f", "fragment"},
		{(&url.URL{Scheme: "http", User: url.UserPassword("u", password), Host: host}).String(), "user info"},
	} {
		for _, source := range []string{"--server", "AF_URL"} {
			t.Run(source+" "+c.url, func(t *testing.T) {
				clientHome(t, str("token = \"T\"\n"))
				t.Setenv("AF_URL", "")
				args := []string{"workspace", "list"}
				if source == "--server" {
					args = append(args, "--server", c.url)
				} else {
					t.Setenv("AF_URL", c.url)
				}
				res := afRun(t, "", args...)
				if res.code != 2 || field(res.env, "status") != "usage" {
					t.Fatalf("exit %d, status %v, want usage and 2", res.code, field(res.env, "status"))
				}
				if !strings.Contains(res.message(), source) || !strings.Contains(res.message(), c.rule) {
					t.Errorf("message %q, want it to name %s and the rule (%s)", res.message(), source, c.rule)
				}
				if strings.Contains(res.output(), password) {
					t.Errorf("the output holds the password of the URL:\n%s", res.output())
				}
				if n := len(svc.Requests()); n != 0 {
					t.Errorf("%d requests were sent", n)
				}
			})
		}
	}
}

// TestNoServerIsAUsageErrorNamingTheFlagAndTheVariable_TS_16_20 verifies TS-16-20 for a
// command without a local mode: the hint is the flag and the variable. The message of
// triage and fix also names --local; its wording comes from noServerFailure.
// Verifies: 16-REQ-3.4
func TestNoServerIsAUsageErrorNamingTheFlagAndTheVariable_TS_16_20(t *testing.T) {
	svc := newFakeService(t, nil)
	clientHome(t, str("token = \"T\"\n"))
	t.Setenv("AF_URL", "")
	res := afRun(t, "", "workspace", "list")
	if res.code != 2 {
		t.Fatalf("exit %d, want 2", res.code)
	}
	hint, _ := field(res.env, "error", "fix_hint").(map[string]any)
	if len(hint) != 2 || hint["flag"] != "--server" || hint["env"] != "AF_URL" {
		t.Errorf("fix_hint = %v, want {flag: --server, env: AF_URL}", hint)
	}
	if m := res.message(); strings.Contains(m, "--local") || !strings.Contains(m, "--server") || !strings.Contains(m, "AF_URL") {
		t.Errorf("message %q, want it to name --server and AF_URL and not --local", m)
	}
	if n := len(svc.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}

	withLocal := noServerFailure(true)
	if !strings.Contains(withLocal.Message, "--local") || !strings.Contains(withLocal.Message, "--server") || !strings.Contains(withLocal.Message, "AF_URL") {
		t.Errorf("the message for triage and fix is %q, want it to name --server, AF_URL and --local", withLocal.Message)
	}
	if h := withLocal.FixHint; h == nil || h.Flag != "--server" || h.Env != "AF_URL" {
		t.Errorf("the fix hint for triage and fix is %+v, want {flag: --server, env: AF_URL}", h)
	}
}

// TestAPlainHTTPServerThatIsNotLoopbackRaisesServerInsecure_TS_16_21 verifies TS-16-21:
// only http://example.com raises the warning, and every command still completes.
// Verifies: 16-REQ-3.5
func TestAPlainHTTPServerThatIsNotLoopbackRaisesServerInsecure_TS_16_21(t *testing.T) {
	useTransport(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"workspaces":[]}`)), Request: r,
		}, nil
	}))
	clientHome(t, str("token = \"T\"\n"))
	t.Setenv("AF_URL", "")
	for _, u := range []string{"http://example.com", "http://localhost:1", "http://127.0.0.2:1", "http://[::1]:1", "https://example.com"} {
		res := afRun(t, "", "workspace", "list", "--server", u)
		if res.code != 0 {
			t.Errorf("--server %s: exit %d, %s", u, res.code, res.stdout)
			continue
		}
		if got, want := slices.Contains(codes(res.env), "server_insecure"), u == "http://example.com"; got != want {
			t.Errorf("--server %s: server_insecure raised = %v, want %v (warnings %v)", u, got, want, codes(res.env))
		}
	}
}

// TestTheRequestCarriesTheTopLevelTokenOfTheCredentialFile_TS_16_22 verifies TS-16-22:
// the token at the top of the file is the credential, whatever else the file holds.
// Verifies: 16-REQ-4.1
func TestTheRequestCarriesTheTopLevelTokenOfTheCredentialFile_TS_16_22(t *testing.T) {
	svc := newFakeService(t, nil)
	clientHome(t, str("note = \"x\"\nretries = 3\ntoken = \"T1\"\n\n[profile]\ntoken = \"T2\"\n[profile.other]\nnote = \"y\"\n"))
	t.Setenv("AF_URL", svc.URL)
	if res := afRun(t, "", "workspace", "list"); res.code != 0 {
		t.Fatalf("exit %d, %s", res.code, res.stdout)
	}
	reqs := svc.Requests()
	if len(reqs) != 1 || reqs[0].Header.Get("Authorization") != "Bearer T1" {
		t.Errorf("requests %+v, want one with Authorization: Bearer T1", reqs)
	}
}

// TestAnUnfindableHomeOrAnUnreadableCredentialFileFailsWithIOBeforeAnyRequest_TS_16_23
// verifies TS-16-23: the home directory cannot be found, the path is a directory, and the
// file has no permission bit.
// Verifies: 16-REQ-4.2
func TestAnUnfindableHomeOrAnUnreadableCredentialFileFailsWithIOBeforeAnyRequest_TS_16_23(t *testing.T) {
	check := func(t *testing.T, svc *fakeService, path string) {
		t.Helper()
		res := afRun(t, "", "workspace", "list")
		if res.code != 1 || res.category() != "io" {
			t.Fatalf("exit %d, category %q, message %q, want 1 and io", res.code, res.category(), res.message())
		}
		if !strings.Contains(res.message(), path) {
			t.Errorf("message %q does not name %s", res.message(), path)
		}
		if n := len(svc.Requests()); n != 0 {
			t.Errorf("%d requests were sent", n)
		}
	}

	t.Run("no home directory", func(t *testing.T) {
		svc := newFakeService(t, nil)
		t.Setenv("AF_URL", svc.URL)
		t.Setenv("HOME", "")
		check(t, svc, ".af/config.toml")
	})
	t.Run("the path is a directory", func(t *testing.T) {
		svc := newFakeService(t, nil)
		t.Setenv("AF_URL", svc.URL)
		_, path := clientHome(t, nil)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		check(t, svc, path)
	})
	t.Run("the file has no permission", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("file modes do not keep the owner out")
		}
		svc := newFakeService(t, nil)
		t.Setenv("AF_URL", svc.URL)
		_, path := clientHome(t, str("token = \"T\"\n"))
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		check(t, svc, path)
	})
}

// TestAMissingFileOrAMissingNonStringOrEmptyTokenFailsAuthWithTheLoginHint_TS_16_24 verifies
// TS-16-24.
// Verifies: 16-REQ-4.3
func TestAMissingFileOrAMissingNonStringOrEmptyTokenFailsAuthWithTheLoginHint_TS_16_24(t *testing.T) {
	for name, content := range map[string]*string{
		"no file":      nil,
		"no token key": str("other = 1\n"),
		"not a string": str("token = 5\n"),
		"empty":        str("token = \"\"\n"),
		"blank":        str("token = \"   \"\n"),
		"in a table":   str("[profile]\ntoken = \"T\"\n"),
	} {
		t.Run(name, func(t *testing.T) {
			svc := newFakeService(t, nil)
			t.Setenv("AF_URL", svc.URL)
			_, path := clientHome(t, content)
			res := afRun(t, "", "workspace", "list")
			if res.code != 1 || res.category() != "auth" {
				t.Fatalf("exit %d, category %q, message %q, want 1 and auth", res.code, res.category(), res.message())
			}
			for _, want := range []string{path, "af login --token-file FILE", "afc login"} {
				if !strings.Contains(res.message(), want) {
					t.Errorf("message %q does not contain %q", res.message(), want)
				}
			}
			hint, _ := field(res.env, "error", "fix_hint").(map[string]any)
			if len(hint) != 1 || hint["suggest"] != "af login --token-file FILE" {
				t.Errorf("fix_hint = %v, want {suggest: af login --token-file FILE}", hint)
			}
			if n := len(svc.Requests()); n != 0 {
				t.Errorf("%d requests were sent", n)
			}
		})
	}
}

// TestACredentialFileThatIsNotTOMLOrATokenWithAControlCharacterFailsConfig_TS_16_25 verifies
// TS-16-25: the message names the file and never echoes what the file holds.
// Verifies: 16-REQ-4.4
func TestACredentialFileThatIsNotTOMLOrATokenWithAControlCharacterFailsConfig_TS_16_25(t *testing.T) {
	for name, content := range map[string]string{
		"not TOML":          "token = \"SENTINEL\n",
		"a line break":      "token = \"SENTINEL\\nSENTINEL\"\n",
		"a control char":    "token = \"SENTINEL\\u0007SENTINEL\"\n",
		"a trailing return": "token = \"SENTINEL\\r\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			svc := newFakeService(t, nil)
			t.Setenv("AF_URL", svc.URL)
			_, path := clientHome(t, str(content))
			res := afRun(t, "", "workspace", "list")
			if res.code != 1 || res.category() != "config" {
				t.Fatalf("exit %d, category %q, message %q, want 1 and config", res.code, res.category(), res.message())
			}
			if !strings.Contains(res.message(), path) {
				t.Errorf("message %q does not name %s", res.message(), path)
			}
			if strings.Contains(res.output(), "SENTINEL") {
				t.Errorf("the output echoes the token:\n%s", res.output())
			}
			if n := len(svc.Requests()); n != 0 {
				t.Errorf("%d requests were sent", n)
			}
		})
	}
}

// TestAGroupOrOtherAccessibleCredentialFileRaisesTheWarningAndTheCommandGoesOn_TS_16_26
// verifies TS-16-26.
// Verifies: 16-REQ-4.5
func TestAGroupOrOtherAccessibleCredentialFileRaisesTheWarningAndTheCommandGoesOn_TS_16_26(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are a Unix notion")
	}
	for _, c := range []struct {
		mode os.FileMode
		warn bool
	}{{0o644, true}, {0o640, true}, {0o604, true}, {0o600, false}, {0o400, false}} {
		svc := newFakeService(t, nil)
		t.Setenv("AF_URL", svc.URL)
		_, path := clientHome(t, str("token = \"T\"\n"))
		if err := os.Chmod(path, c.mode); err != nil {
			t.Fatal(err)
		}
		res := afRun(t, "", "workspace", "list")
		if res.code != 0 {
			t.Errorf("mode %o: exit %d, %s", c.mode, res.code, res.stdout)
			continue
		}
		if got := slices.Contains(codes(res.env), "credential_file_permissions"); got != c.warn {
			t.Errorf("mode %o: credential_file_permissions raised = %v, want %v", c.mode, got, c.warn)
		}
		if c.warn && !strings.Contains(res.stdout, path) {
			t.Errorf("mode %o: the warning does not name %s:\n%s", c.mode, path, res.stdout)
		}
	}
}
