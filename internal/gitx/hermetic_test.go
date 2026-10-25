package gitx

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentkit-go/tools"
)

func TestHermeticEnv(t *testing.T) {
	realHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realHome, "go", "pkg", "mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := []string{
		"HOME=" + realHome, "PATH=/bin", "CARGO_HOME=/opt/cargo", "XDG_CONFIG_HOME=/home/me/.config",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=init.defaultBranch", "GIT_CONFIG_VALUE_0=main", "GIT_CONFIG_PARAMETERS='a.b=c'",
		"GIT_DIR=/elsewhere/.git", "GIT_INDEX_FILE=/tmp/index", "GIT_AUTHOR_NAME=Leaky", "GIT_COMMITTER_EMAIL=leak@example.com", "EMAIL=me@example.com",
		"GIT_TERMINAL_PROMPT=1", "GIT_ASKPASS=/opt/editor/askpass.sh", "SSH_ASKPASS=/usr/libexec/ssh-askpass", "SSH_ASKPASS_REQUIRE=force",
	}

	env := HermeticEnv(base, "/clean")

	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, twice := got[k]; twice {
			t.Errorf("%s is set twice", k)
		}
		got[k] = v
	}
	want := map[string]string{
		"HOME": "/clean", "XDG_CONFIG_HOME": filepath.Join("/clean", ".config"), "PATH": "/bin",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0",
		// The download caches stay where they are: one that is set is kept, and
		// a default that exists is pinned.
		"CARGO_HOME": "/opt/cargo", "GOPATH": filepath.Join(realHome, "go"), "GOMODCACHE": filepath.Join(realHome, "go", "pkg", "mod"),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{
		"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_PARAMETERS", "GIT_DIR", "GIT_INDEX_FILE",
		"GIT_AUTHOR_NAME", "GIT_COMMITTER_EMAIL", "EMAIL", "GIT_ASKPASS", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE",
		"RUSTUP_HOME", // its default directory does not exist, and is not invented
	} {
		if v, ok := got[k]; ok {
			t.Errorf("%s = %q, want it gone", k, v)
		}
	}
}

// hermetic is the runner of a worker's git: the reduced environment, made
// hermetic around home.
func hermetic(home string) Runner {
	return ExplicitEnvRunner(HermeticEnv(tools.ReducedEnv(nil), home))
}

// A fixture that relies on the author's ~/.gitconfig (init.defaultBranch, an
// email) passes on the author's machine and fails on a clean image; under the
// hermetic environment it fails everywhere.
func TestHermeticGitReadsNoConfigurationOfTheUser(t *testing.T) {
	testutil.Require(t, "git", "sh")
	userHome := t.TempDir()
	config := testutil.WriteFile(t, userHome, ".gitconfig", "[init]\n\tdefaultBranch = trunk\n[user]\n\temail = me@example.com\n")
	t.Setenv("HOME", userHome)
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_AUTHOR_NAME", "Leaky Author")
	dir, clean := t.TempDir(), t.TempDir()
	if out, _, _ := ExecRunner(t.Context(), dir, []string{"git", "config", "--get", "init.defaultBranch"}); strings.TrimSpace(out) != "trunk" {
		t.Fatalf("plain git does not read the fixture's configuration: %q", out)
	}

	for _, key := range []string{"init.defaultBranch", "user.email"} {
		if out, code, err := hermetic(clean)(t.Context(), dir, []string{"git", "config", "--get", key}); err != nil || code != 1 {
			t.Errorf("git config --get %s = %q, exit code %d, %v; want it unset", key, out, code, err)
		}
	}
	if out, _, _ := hermetic(clean)(t.Context(), dir, []string{"sh", "-c", "echo $HOME:$GIT_AUTHOR_NAME"}); strings.TrimSpace(out) != clean+":" {
		t.Errorf("HOME and identity = %q, want %q", strings.TrimSpace(out), clean+":")
	}
}

// A forge that wants a credential makes git ask the askpass program, then the
// terminal, and an editor's askpass waits for an answer until the gate times
// out. Under the hermetic environment git asks neither and fails at once.
func TestHermeticGitNeverAsksForACredential(t *testing.T) {
	testutil.Require(t, "git")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="forge"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	asked := filepath.Join(t.TempDir(), "asked")
	askpass := testutil.WriteFile(t, t.TempDir(), "askpass.sh", "#!/bin/sh\necho \"$1\" >> '"+asked+"'\necho someone\n")
	if err := os.Chmod(askpass, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_ASKPASS", askpass)
	t.Setenv("SSH_ASKPASS", askpass)
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	t.Setenv("GIT_ALLOW_PROTOCOL", "http") // a test binary may bar every transport but file
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	t.Setenv("no_proxy", "127.0.0.1,localhost")

	out, code, err := hermetic(t.TempDir())(t.Context(), t.TempDir(), []string{"git", "ls-remote", "--heads", srv.URL + "/acme/widgets.git", "main"})

	if err != nil || code == 0 {
		t.Fatalf("git ls-remote = %q, exit code %d, %v; want git to run and fail", out, code, err)
	}
	if _, err := os.Stat(asked); err == nil {
		t.Errorf("the askpass program was asked: %q", testutil.ReadFile(t, asked))
	}
	if !strings.Contains(out, "terminal prompts disabled") {
		t.Errorf("git ls-remote = %q, want it to refuse to prompt", out)
	}
}
