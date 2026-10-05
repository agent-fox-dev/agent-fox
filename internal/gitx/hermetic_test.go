package gitx

import (
	"context"
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
