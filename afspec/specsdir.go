package afspec

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultSpecDirName is where spec packages live under the repository root.
const DefaultSpecDirName = ".specs"

// SpecDirEnv overrides the spec root. A tool's --specs-dir flag wins over it.
const SpecDirEnv = "AF_SPEC_DIR"

// ResolveSpecsDir picks the spec root: the flag, then the environment, then
// the repository's own .specs. A relative value is taken against root.
func ResolveSpecsDir(flag, root string) string {
	pick := func(v string) string {
		if filepath.IsAbs(v) {
			return filepath.Clean(v)
		}
		return filepath.Join(root, v)
	}
	if v := strings.TrimSpace(flag); v != "" {
		return pick(v)
	}
	if v := strings.TrimSpace(os.Getenv(SpecDirEnv)); v != "" {
		return pick(v)
	}
	return filepath.Join(root, DefaultSpecDirName)
}
