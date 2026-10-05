package checks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
)

// Environment is the fingerprint of the environment a verification ran in:
// what a reader needs in order to reproduce "the checks pass", and what
// tells a clean run from one that leaned on the author's configuration.
type Environment struct {
	// Hermetic is true when the checks ran under gitx.HermeticRunner: an
	// empty HOME and no global or system git configuration.
	Hermetic bool `json:"hermetic" description:"True when the checks ran with an empty HOME and no global or system git configuration."`
	// GitVersion is `git --version`, as the checks' environment saw it.
	GitVersion string `json:"git_version,omitempty" trust:"fact" description:"git --version, as the checks' environment saw it."`
	// InitDefaultBranch is init.defaultBranch as that environment resolves
	// it, or "unset" when nothing sets it and git falls back to its own
	// default.
	InitDefaultBranch string `json:"init_default_branch,omitempty" trust:"fact" description:"init.defaultBranch as that environment resolves it, or unset."`
	// GoVersion is `go version`, for a repository with a go.mod.
	GoVersion string `json:"go_version,omitempty" trust:"fact" description:"go version, for a repository with a go.mod."`
}

// Fingerprint asks the environment r runs in what it is. Each probe that
// cannot run is left empty rather than guessed.
func Fingerprint(ctx context.Context, r gitx.Runner, dir string, hermetic bool) Environment {
	if r == nil {
		r = gitx.ReducedEnvRunner
	}
	probe := func(argv ...string) (string, int) {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out, code, err := r(pctx, dir, argv)
		if err != nil {
			return "", -1
		}
		return strings.TrimSpace(out), code
	}
	env := Environment{Hermetic: hermetic}
	if out, code := probe("git", "--version"); code == 0 {
		env.GitVersion = out
	}
	switch out, code := probe("git", "config", "--get", "init.defaultBranch"); {
	case code == 0 && out != "":
		env.InitDefaultBranch = out
	case code == 1:
		env.InitDefaultBranch = "unset"
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		if out, code := probe("go", "version"); code == 0 {
			env.GoVersion = out
		}
	}
	return env
}
