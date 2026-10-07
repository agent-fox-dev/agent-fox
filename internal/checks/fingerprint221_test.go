package checks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #221: the clean-environment fingerprint names the toolchain of every
// ecosystem the repository has, not only Go's.
func TestTheFingerprintNamesEveryToolchain(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"pyproject.toml", "package.json", "Cargo.toml"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := func(_ context.Context, _ string, argv []string, _ ...string) (string, int, error) {
		switch strings.Join(argv, " ") {
		case "python3 --version":
			return "Python 3.13.1\n", 0, nil
		case "node --version":
			return "v22.4.0\n", 0, nil
		case "rustc --version":
			return "rustc 1.86.0 (abc 2026-01-01)\nextra\n", 0, nil
		}
		return "", 1, nil
	}
	env := Fingerprint(context.Background(), r, dir, true)
	for lang, want := range map[string]string{"python": "Python 3.13.1", "node": "v22.4.0", "rust": "rustc 1.86.0 (abc 2026-01-01)"} {
		if env.Toolchains[lang] != want {
			t.Errorf("toolchains[%s] = %q, want %q (all %v)", lang, env.Toolchains[lang], want, env.Toolchains)
		}
	}
}
