package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/agent-fox-dev/agentfox/internal/afclient"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
)

// afRunReader is afRun with a stdin that is any reader.
func afRunReader(t *testing.T, stdin io.Reader, args ...string) afResult {
	t.Helper()
	var out, errb bytes.Buffer
	code := newApp().Main(context.Background(), append([]string{"af"}, args...), stdin, &out, &errb)
	res := afResult{code: code, stdout: out.String(), stderr: errb.String()}
	if out.Len() > 0 {
		res.env = oneDocument(t, res.stdout)
	}
	return res
}

// tokenFile writes content to a file of its own and returns its path.
func tokenFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// readFile is the content of a file, or "<absent>" when there is none.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "<absent>"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// authorizations are the Authorization headers the service saw, in order.
func (s *fakeService) authorizations() []string {
	var out []string
	for _, r := range s.Requests() {
		out = append(out, r.Header.Get("Authorization"))
	}
	return out
}

// TestLoginReadsTheCredentialFromAFileOrStdinNeverTheCommandLineAndAnUnreadableSourceIsIO_TS_16_29
// verifies TS-16-29.
// Verifies: 16-REQ-5.1
func TestLoginReadsTheCredentialFromAFileOrStdinNeverTheCommandLineAndAnUnreadableSourceIsIO_TS_16_29(t *testing.T) {
	svc := newFakeService(t, nil)
	clientHome(t, nil)
	t.Setenv("AF_URL", svc.URL)

	file := tokenFile(t, "file-token\n")
	if res := afRun(t, "", "login", "--token-file", file); res.code != 0 {
		t.Fatalf("a token file: exit %d, %s", res.code, res.stdout)
	}
	if res := afRun(t, "stdin-token\n", "login", "--token-file", "-"); res.code != 0 {
		t.Fatalf("stdin: exit %d, %s", res.code, res.stdout)
	}
	if got := svc.authorizations(); len(got) != 2 || got[0] != "Bearer file-token" || got[1] != "Bearer stdin-token" {
		t.Errorf("the check requests carried %v, want the token of the file and then of stdin", got)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	res := afRun(t, "", "login", "--token-file", missing)
	if res.code != 1 || res.category() != "io" || !strings.Contains(res.message(), missing) {
		t.Errorf("a missing file: exit %d, category %q, message %q, want 1, io, naming %s", res.code, res.category(), res.message(), missing)
	}
	res = afRunReader(t, failingReader{}, "login", "--token-file", "-")
	if res.code != 1 || res.category() != "io" || !strings.Contains(res.message(), "stdin") {
		t.Errorf("an unreadable stdin: exit %d, category %q, message %q, want 1, io, naming stdin", res.code, res.category(), res.message())
	}
	res = afRun(t, "", "login", "--token-file", file, "extra-token")
	if res.code != 2 || !strings.Contains(res.message(), "extra-token") {
		t.Errorf("a positional argument: exit %d, message %q, want 2 refusing it", res.code, res.message())
	}
	res = afRun(t, "", "login")
	if res.code != 2 || !strings.Contains(res.message(), "--token-file") {
		t.Errorf("no --token-file: exit %d, message %q, want 2 naming the flag", res.code, res.message())
	}
	if n := len(svc.Requests()); n != 2 {
		t.Errorf("%d requests, want the 2 checks of the two logins", n)
	}
}

// failingReader is a stdin that cannot be read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("the pipe is broken") }

// TestLoginRefusesAnEmptyMultiLineControlOrOversizedCredentialAndWritesNothing_TS_16_30
// verifies TS-16-30.
// Verifies: 16-REQ-5.2
func TestLoginRefusesAnEmptyMultiLineControlOrOversizedCredentialAndWritesNothing_TS_16_30(t *testing.T) {
	for name, content := range map[string]string{
		"blank":     " \n\t\n",
		"two lines": "first\nsecond\n",
		"control":   "ab\x01cd",
		"oversized": strings.Repeat("x", 4097),
	} {
		t.Run(name, func(t *testing.T) {
			svc := newFakeService(t, nil)
			_, config := clientHome(t, nil)
			t.Setenv("AF_URL", svc.URL)
			res := afRun(t, "", "login", "--token-file", tokenFile(t, content))
			if res.code != 2 || !strings.Contains(res.message(), "--token-file") {
				t.Errorf("exit %d, message %q, want 2 naming --token-file", res.code, res.message())
			}
			if n := len(svc.Requests()); n != 0 {
				t.Errorf("%d requests were sent", n)
			}
			if got := readFile(t, config); got != "<absent>" {
				t.Errorf("the credential file holds %q, want it absent", got)
			}
			if strings.Contains(res.output(), "ab\x01cd") {
				t.Error("the output holds the credential")
			}
		})
	}

	svc := newFakeService(t, nil)
	clientHome(t, nil)
	t.Setenv("AF_URL", svc.URL)
	for content, want := range map[string]string{
		strings.Repeat("x", 4096): strings.Repeat("x", 4096),
		"  tok \n":                "tok",
		"crlf\r\n":                "crlf",
	} {
		if res := afRun(t, "", "login", "--token-file", tokenFile(t, content)); res.code != 0 {
			t.Errorf("%.20q: exit %d, %s", content, res.code, res.message())
		}
		got := svc.authorizations()
		if len(got) == 0 || got[len(got)-1] != "Bearer "+want {
			t.Errorf("%.20q: the checks carried %.30q, want the last to carry the trimmed token", content, got)
		}
	}
}

// TestLoginRefusesAnExistingFileItCannotReadParseOrEditAndLeavesItAsItWas_TS_16_31 verifies
// TS-16-31.
// Verifies: 16-REQ-5.3
func TestLoginRefusesAnExistingFileItCannotReadParseOrEditAndLeavesItAsItWas_TS_16_31(t *testing.T) {
	for _, c := range []struct {
		name, content string
		unreadable    bool
		category      string
		layout        bool
	}{
		{"unreadable", "token = \"old\"\n", true, "io", false},
		{"invalid TOML", "token = \"old\n", false, "config", false},
		{"no token line", "other = 1\n", false, "config", true},
		{"token after a table", "[profile]\ntoken = \"old\"\n", false, "config", true},
		{"a multi-line string", "token = \"\"\"old\nolder\"\"\"\n", false, "config", true},
		{"two strings", "token = \"a\" \"b\"\n", false, "config", false},
		{"a dotted key", "token.x = \"old\"\n", false, "config", true},
		{"a quoted key", "\"token\" = \"old\"\n", false, "config", true},
		{"a token in a decoy string", "notes = \"\"\"\ntoken = \"decoy\"\n\"\"\"\n", false, "config", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.unreadable && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("file modes do not keep the owner out")
			}
			svc := newFakeService(t, nil)
			_, config := clientHome(t, str(c.content))
			t.Setenv("AF_URL", svc.URL)
			if c.unreadable {
				if err := os.Chmod(config, 0); err != nil {
					t.Fatal(err)
				}
			}
			res := afRun(t, "", "login", "--token-file", tokenFile(t, "new-token"))
			if res.code != 1 || res.category() != c.category {
				t.Fatalf("exit %d, category %q, message %q, want 1 and %s", res.code, res.category(), res.message(), c.category)
			}
			if !strings.Contains(res.message(), config) {
				t.Errorf("message %q does not name %s", res.message(), config)
			}
			if c.layout {
				for _, want := range []string{"layout", "not the one af expects", "afc login", "edit"} {
					if !strings.Contains(res.message(), want) {
						t.Errorf("message %q does not contain %q", res.message(), want)
					}
				}
			}
			if n := len(svc.Requests()); n != 0 {
				t.Errorf("%d requests were sent", n)
			}
			if c.unreadable {
				if err := os.Chmod(config, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := readFile(t, config); got != c.content {
				t.Errorf("the file holds %q, want it as it was: %q", got, c.content)
			}
			if strings.Contains(res.output(), "new-token") {
				t.Error("the output holds the credential")
			}
		})
	}
}

// TestLoginChecksTheNewCredentialWithOneGetOfTheWorkspacesAndAcceptsAny2xx_TS_16_32 verifies
// TS-16-32.
// Verifies: 16-REQ-5.4
func TestLoginChecksTheNewCredentialWithOneGetOfTheWorkspacesAndAcceptsAny2xx_TS_16_32(t *testing.T) {
	for _, status := range []int{200, 201, 204} {
		svc := newFakeService(t, answering(status, ``))
		_, config := clientHome(t, nil)
		t.Setenv("AF_URL", svc.URL)
		res := afRun(t, "", "login", "--token-file", tokenFile(t, "new-token"))
		if res.code != 0 {
			t.Errorf("%d: exit %d, %s", status, res.code, res.message())
		}
		reqs := svc.Requests()
		if len(reqs) != 1 || reqs[0].Method != "GET" || reqs[0].Path != "/api/v1/workspaces" || reqs[0].Header.Get("Authorization") != "Bearer new-token" {
			t.Errorf("%d: requests %+v, want one GET /api/v1/workspaces with the new token", status, reqs)
		}
		if got := readFile(t, config); got != "token = \"new-token\"\n" {
			t.Errorf("%d: the credential file holds %q", status, got)
		}
	}
}

// TestLoginFailsAuthWhenTheServiceRefusesTheCredentialAndWritesNothing_TS_16_33 verifies
// TS-16-33.
// Verifies: 16-REQ-5.5
func TestLoginFailsAuthWhenTheServiceRefusesTheCredentialAndWritesNothing_TS_16_33(t *testing.T) {
	for name, existing := range map[string]*string{"no file": nil, "an existing file": str("# mine\ntoken = \"old\"\n")} {
		t.Run(name, func(t *testing.T) {
			svc := newFakeService(t, answering(401, `{"error":{"code":"unauthenticated","message":"no"}}`))
			home, config := clientHome(t, existing)
			t.Setenv("AF_URL", svc.URL)
			before := readFile(t, config)
			res := afRun(t, "", "login", "--token-file", tokenFile(t, "new-token"))
			if res.code != 1 || res.category() != "auth" || !strings.Contains(res.message(), "refused") {
				t.Errorf("exit %d, category %q, message %q, want 1, auth, saying that the service refused the credential", res.code, res.category(), res.message())
			}
			if after := readFile(t, config); after != before {
				t.Errorf("the credential file holds %q, want it as it was: %q", after, before)
			}
			if existing == nil {
				if _, err := os.Stat(filepath.Join(home, ".af")); err == nil {
					t.Error("~/.af was created")
				}
			}
		})
	}
}

// TestLoginSaysTheCredentialCouldNotBeCheckedForAnythingButA401AndWritesNothing_TS_16_34
// verifies TS-16-34.
// Verifies: 16-REQ-5.6
func TestLoginSaysTheCredentialCouldNotBeCheckedForAnythingButA401AndWritesNothing_TS_16_34(t *testing.T) {
	const old = "# mine\ntoken = \"old\"\n"
	check := func(t *testing.T, res afResult, config string, code int, category string, retryable bool) {
		t.Helper()
		if res.code != code || res.category() != category || res.retryable() != retryable {
			t.Errorf("exit %d, category %q, retryable %v, message %q, want %d, %s, %v", res.code, res.category(), res.retryable(), res.message(), code, category, retryable)
		}
		if m := res.message(); !strings.Contains(m, "could not be checked") || strings.Contains(m, "refused") {
			t.Errorf("message %q, want it to say that the credential could not be checked and never that it was refused", m)
		}
		if got := readFile(t, config); got != old {
			t.Errorf("the credential file holds %q, want it as it was", got)
		}
	}
	for _, c := range []struct {
		status    int
		code      int
		category  string
		retryable bool
	}{{502, 1, "hub", true}, {500, 1, "service", false}, {404, 2, "usage", false}, {503, 1, "service", true}} {
		svc := newFakeService(t, answering(c.status, `{"error":{"code":"x","message":"the service says no"}}`))
		_, config := clientHome(t, str(old))
		t.Setenv("AF_URL", svc.URL)
		check(t, afRun(t, "", "login", "--token-file", tokenFile(t, "new-token")), config, c.code, c.category, c.retryable)
	}

	t.Run("a closed port", func(t *testing.T) {
		dead := newFakeService(t, nil)
		dead.Close()
		_, config := clientHome(t, str(old))
		t.Setenv("AF_URL", dead.URL)
		check(t, afRun(t, "", "login", "--token-file", tokenFile(t, "new-token")), config, 1, "service", true)
	})
	t.Run("a service that stalls past the bound", func(t *testing.T) {
		release := make(chan struct{})
		svc := newFakeService(t, func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		t.Cleanup(func() { close(release) })
		_, config := clientHome(t, str(old))
		t.Setenv("AF_URL", svc.URL)
		clk := fakeclock.New(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
		useClock(t, clk)
		wait := startAF(t, "", "login", "--token-file", tokenFile(t, "new-token"))
		if !clk.WaitForWaits(1, 10*time.Second) {
			t.Fatal("the check asked for no bound")
		}
		clk.Advance(2 * time.Minute)
		check(t, wait(), config, 1, "service", true)
	})
}

// TestAFirstLoginWritesMode0600AndALaterOneReplacesOnlyTheTokenValue_TS_16_35 verifies
// TS-16-35: ~/.af is created with 0700, the file is the single line, and a rewrite keeps every
// other byte (comments, other keys, a table, CRLF endings, the comment after the token).
// Verifies: 16-REQ-5.7
func TestAFirstLoginWritesMode0600AndALaterOneReplacesOnlyTheTokenValue_TS_16_35(t *testing.T) {
	svc := newFakeService(t, nil)
	home, config := clientHome(t, nil)
	t.Setenv("AF_URL", svc.URL)
	const cred = `ab"c\d'e` + "ä"

	res := afRun(t, cred+"\n", "login", "--token-file", "-")
	if res.code != 0 {
		t.Fatalf("the first login: exit %d, %s", res.code, res.message())
	}
	if got, want := readFile(t, config), "token = \"ab\\\"c\\\\d'eä\"\n"; got != want {
		t.Errorf("the credential file holds %q, want %q", got, want)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(config); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("the credential file: %v, %v, want mode 0600", info, err)
		}
		if info, err := os.Stat(filepath.Join(home, ".af")); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("~/.af: %v, %v, want mode 0700", info, err)
		}
	}
	checkResult := func(res afResult) {
		t.Helper()
		result, _ := res.env["result"].(map[string]any)
		if len(result) != 2 || result["credential_file"] != config || result["server"] != svc.URL {
			t.Errorf("result = %v, want {credential_file: %s, server: %s}", result, config, svc.URL)
		}
		if strings.Contains(res.output(), cred) || strings.Contains(res.output(), `ab\"c`) {
			t.Errorf("the output holds the credential:\n%s", res.output())
		}
	}
	checkResult(res)

	before := "# my afc settings\r\nname = \"me\"\r\ntoken = 'old'  # note\r\nretries = 3\r\n\r\n[profile]\r\ntoken = \"other\"\r\n"
	if err := os.WriteFile(config, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	res = afRun(t, cred, "login", "--token-file", "-")
	if res.code != 0 {
		t.Fatalf("the second login: exit %d, %s", res.code, res.message())
	}
	want := "# my afc settings\r\nname = \"me\"\r\ntoken = \"ab\\\"c\\\\d'eä\"  # note\r\nretries = 3\r\n\r\n[profile]\r\ntoken = \"other\"\r\n"
	if got := readFile(t, config); got != want {
		t.Errorf("the credential file holds %q, want %q", got, want)
	}
	var doc map[string]any
	if _, err := toml.Decode(readFile(t, config), &doc); err != nil || doc["token"] != cred {
		t.Errorf("the file parses to %v (%v), want the token %q", doc["token"], err, cred)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(config); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("the rewritten file: %v, %v, want mode 0600", info, err)
		}
	}
	checkResult(res)
}

// faultyFS is the file system of the operating system with one step that fails.
type faultyFS struct {
	afclient.FS
	fail string
}

var errInjected = errors.New("injected failure")

func (f faultyFS) MkdirAll(path string, perm fs.FileMode) error {
	if f.fail == "mkdir" {
		return errInjected
	}
	return f.FS.MkdirAll(path, perm)
}

func (f faultyFS) CreateTemp(dir, pattern string) (afclient.File, error) {
	if f.fail == "create" {
		return nil, errInjected
	}
	file, err := f.FS.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return faultyFile{File: file, fail: f.fail}, nil
}

func (f faultyFS) Rename(oldpath, newpath string) error {
	if f.fail == "rename" {
		return errInjected
	}
	return f.FS.Rename(oldpath, newpath)
}

type faultyFile struct {
	afclient.File
	fail string
}

func (f faultyFile) Write(p []byte) (int, error) {
	if f.fail == "write" {
		return 0, errInjected
	}
	return f.File.Write(p)
}

func (f faultyFile) Sync() error {
	if f.fail == "sync" {
		return errInjected
	}
	return f.File.Sync()
}

func (f faultyFile) Chmod(mode fs.FileMode) error {
	if f.fail == "chmod" {
		return errInjected
	}
	return f.File.Chmod(mode)
}

func (f faultyFile) Close() error {
	err := f.File.Close()
	if f.fail == "close" {
		return errInjected
	}
	return err
}

// TestEachWritePhaseFailureIsIONamesThePathRemovesTheTempFileAndKeepsTheOldFile_TS_16_36
// verifies TS-16-36.
// Verifies: 16-REQ-5.8
func TestEachWritePhaseFailureIsIONamesThePathRemovesTheTempFileAndKeepsTheOldFile_TS_16_36(t *testing.T) {
	const old = "# mine\ntoken = \"old\"\n"
	for _, fault := range []string{"mkdir", "create", "write", "sync", "chmod", "close", "rename"} {
		t.Run(fault, func(t *testing.T) {
			svc := newFakeService(t, nil)
			var content *string
			if fault != "mkdir" { // mkdir is only asked for when ~/.af is missing
				content = str(old)
			}
			home, config := clientHome(t, content)
			t.Setenv("AF_URL", svc.URL)
			prev := credentialFS
			credentialFS = faultyFS{FS: afclient.OSFS{}, fail: fault}
			t.Cleanup(func() { credentialFS = prev })

			res := afRun(t, "", "login", "--token-file", tokenFile(t, "new-token"))
			if res.code != 1 || res.category() != "io" || !strings.Contains(res.message(), config) {
				t.Errorf("exit %d, category %q, message %q, want 1, io, naming %s", res.code, res.category(), res.message(), config)
			}
			entries, err := os.ReadDir(filepath.Join(home, ".af"))
			switch {
			case fault == "mkdir":
				if err == nil {
					t.Errorf("~/.af exists with %d entries", len(entries))
				}
			case err != nil || len(entries) != 1 || entries[0].Name() != "config.toml":
				t.Errorf("~/.af holds %v (%v), want only config.toml", entries, err)
			}
			if fault != "mkdir" {
				if got := readFile(t, config); got != old {
					t.Errorf("the credential file holds %q, want it as it was", got)
				}
			}
			if strings.Contains(res.output(), "new-token") {
				t.Error("the output holds the credential")
			}
		})
	}
}

// TestSeveralLocalMistakesReportTheFirstInTheFixedOrderAndSendNothing_TS_16_37 verifies
// TS-16-37: flags, then the arguments and the input, then the server, then the credential (for
// login the existing file), and a usage mistake before an auth or config failure.
// Verifies: 16-REQ-6.1
func TestSeveralLocalMistakesReportTheFirstInTheFixedOrderAndSendNothing_TS_16_37(t *testing.T) {
	svc := newFakeService(t, nil)
	clientHome(t, str("other = 1\n")) // a credential file with no token
	t.Setenv("AF_URL", "")
	blank := tokenFile(t, " \n")
	good := tokenFile(t, "new-token")
	bad := "ftp://" + strings.TrimPrefix(svc.URL, "http://")

	for _, c := range []struct {
		name string
		args []string
		want string // a word of the message of the first mistake
	}{
		{"login: flag, input, server, file", []string{"login", "--bogus", "--token-file", blank, "--server", bad}, "bogus"},
		{"login: input, server, file", []string{"login", "--token-file", blank, "--server", bad}, "--token-file"},
		{"login: server, file", []string{"login", "--token-file", good, "--server", bad}, "http or https"},
		{"login: no server, file", []string{"login", "--token-file", good}, "AF_URL"},
		{"login: the file", []string{"login", "--token-file", good, "--server", svc.URL}, "layout"},
		{"workspace list: flag, server, credential", []string{"workspace", "list", "--bogus", "--server", bad}, "bogus"},
		{"workspace list: server, credential", []string{"workspace", "list", "--server", bad}, "http or https"},
		{"workspace list: no server, credential", []string{"workspace", "list"}, "AF_URL"},
		{"workspace list: the credential", []string{"workspace", "list", "--server", svc.URL}, "token"},
	} {
		res := afRun(t, "", c.args...)
		if res.code == 0 || !strings.Contains(res.message(), c.want) {
			t.Errorf("%s: exit %d, message %q, want it to name %q", c.name, res.code, res.message(), c.want)
		}
	}
	if n := len(svc.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}
