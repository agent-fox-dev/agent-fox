package checks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
)

func TestFingerprintReportsWhatTheEnvironmentResolves(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	userHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(userHome, ".gitconfig"), []byte("[init]\n\tdefaultBranch = trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", userHome)
	for _, k := range []string{"XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM"} {
		t.Setenv(k, "") // restored after the test
		os.Unsetenv(k)
	}
	dir := t.TempDir()
	ctx := context.Background()

	leaky := Fingerprint(ctx, gitx.ReducedEnvRunner, dir, false)
	if leaky.Hermetic || leaky.InitDefaultBranch != "trunk" || leaky.GitVersion == "" {
		t.Errorf("fingerprint of the user's environment = %+v", leaky)
	}
	clean := Fingerprint(ctx, gitx.HermeticRunner(t.TempDir()), dir, true)
	if !clean.Hermetic || clean.InitDefaultBranch != "unset" || clean.GitVersion == "" {
		t.Errorf("fingerprint of the hermetic environment = %+v", clean)
	}
	if clean.GoVersion != "" {
		t.Errorf("GoVersion = %q for a directory with no go.mod", clean.GoVersion)
	}
}
