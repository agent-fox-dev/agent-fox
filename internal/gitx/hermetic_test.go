package gitx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A user's global git configuration is what makes a fixture that calls
// `git init` without -b pass on the author's machine and fail on a clean
// image. Under the hermetic runner the configuration is not read at all.
func TestHermeticRunnerIgnoresTheUsersGitConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	userHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(userHome, ".gitconfig"),
		[]byte("[init]\n\tdefaultBranch = trunk\n[user]\n\temail = me@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", userHome)
	for _, k := range []string{"XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM"} {
		t.Setenv(k, "") // restored after the test
		os.Unsetenv(k)
	}
	t.Setenv("GIT_AUTHOR_NAME", "Leaky Author")
	ctx := context.Background()
	dir := t.TempDir()

	out, code, err := ExecRunner(ctx, dir, []string{"git", "config", "--get", "init.defaultBranch"})
	if err != nil || code != 0 || strings.TrimSpace(out) != "trunk" {
		t.Fatalf("the fixture's own HOME is not read by plain git: %q (%d) %v", out, code, err)
	}

	clean := t.TempDir()
	r := HermeticRunner(clean)
	for _, key := range []string{"init.defaultBranch", "user.email"} {
		out, code, err := r(ctx, dir, []string{"git", "config", "--get", key})
		if err != nil || code != 1 {
			t.Errorf("hermetic git config --get %s = %q (exit %d, %v); want unset (exit 1)", key, out, code, err)
		}
	}
	out, _, err = r(ctx, dir, []string{"sh", "-c", "echo $HOME:$GIT_AUTHOR_NAME"})
	if err != nil || strings.TrimSpace(out) != clean+":" {
		t.Errorf("hermetic HOME and identity = %q, want %q", strings.TrimSpace(out), clean+":")
	}
}

func TestHermeticEnvKeepsToolchainCachesWhereTheyAre(t *testing.T) {
	realHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realHome, "go", "pkg", "mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := HermeticEnv([]string{"HOME=" + realHome, "PATH=/bin", "GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=init.defaultBranch", "GIT_CONFIG_VALUE_0=main", "CARGO_HOME=/opt/cargo"}, "/clean")
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, dup := got[k]; dup {
			t.Errorf("%s is set twice", k)
		}
		got[k] = v
	}
	want := map[string]string{
		"HOME": "/clean", "PATH": "/bin", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.DevNull,
		"GOPATH": filepath.Join(realHome, "go"), "GOMODCACHE": filepath.Join(realHome, "go", "pkg", "mod"),
		"CARGO_HOME": "/opt/cargo",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "RUSTUP_HOME"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s = %q survived; a clean environment does not have it", k, v)
		}
	}
}

// A test that reaches a forge over https with no credential helper makes git
// ask for a username: the askpass program first, then the terminal. On a clean
// CI image there is neither and the command fails at once. Started from an
// editor's terminal, git asked the editor's askpass, which showed a dialog and
// waited until the gate's timeout. Under the hermetic runner neither is asked.
func TestHermeticRunnerNeverAsksForACredential(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="forge"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	asked := filepath.Join(t.TempDir(), "asked")
	askpass := filepath.Join(t.TempDir(), "askpass.sh")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\necho \"$1\" >> '"+asked+"'\necho someone\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_ASKPASS", askpass)
	t.Setenv("SSH_ASKPASS", askpass)
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	// The test needs git to reach the server; a test binary that runs this
	// suite may have barred every transport but file (envtest.NoGitNetwork).
	t.Setenv("GIT_ALLOW_PROTOCOL", "")
	if err := os.Unsetenv("GIT_ALLOW_PROTOCOL"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	t.Setenv("no_proxy", "127.0.0.1,localhost")

	out, code, err := HermeticRunner(t.TempDir())(context.Background(), t.TempDir(),
		[]string{"git", "ls-remote", "--heads", srv.URL + "/acme/widgets.git", "main"})
	if err != nil {
		t.Fatalf("git ls-remote did not run: %v", err)
	}
	if code == 0 {
		t.Fatalf("git ls-remote against a server that wants a credential succeeded: %q", out)
	}
	if b, err := os.ReadFile(asked); err == nil {
		t.Errorf("the askpass program was asked for %q; a clean environment has none", strings.TrimSpace(string(b)))
	}
	if !strings.Contains(out, "terminal prompts disabled") {
		t.Errorf("git ls-remote output = %q, want git to refuse to prompt (terminal prompts disabled)", out)
	}
}

func TestHermeticEnvDisablesCredentialPrompts(t *testing.T) {
	env := HermeticEnv([]string{"HOME=/home/me", "PATH=/bin", "GIT_TERMINAL_PROMPT=1",
		"GIT_ASKPASS=/opt/editor/askpass.sh", "SSH_ASKPASS=/usr/libexec/ssh-askpass", "SSH_ASKPASS_REQUIRE=force"}, "/clean")
	got := map[string][]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = append(got[k], v)
	}
	if v := got["GIT_TERMINAL_PROMPT"]; len(v) != 1 || v[0] != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q, want exactly one value, 0", v)
	}
	for _, k := range []string{"GIT_ASKPASS", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s = %q survived; a clean environment has no one to ask", k, v)
		}
	}
}
