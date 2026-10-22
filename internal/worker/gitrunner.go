package worker

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/hub"
)

const (
	// gitUser is the user name of the Basic credential that hub's git server takes; its
	// password is the PAT of the subscription.
	gitUser = "agent-fox"
	// committerName and committerEmail are the identity of the commits an attempt makes, set in
	// the local configuration of the clone and nowhere else.
	committerName  = "agent-fox"
	committerEmail = "agent-fox@noreply.invalid"
)

// gitRunner is the runner of every git command of an attempt: the clone, the git commands of the
// fix pipeline and the push. The commands run in a clean environment and nothing of the machine's
// own git configuration, no prompt and no askpass program reach them, so that no credential
// helper of the operator is ever asked for the PAT or answers for it.
//
// A command that can reach a remote also carries the PAT, and nothing else does. The PAT is only
// the header Authorization: Basic base64(agent-fox:<PAT>), set as git configuration scoped to the
// clone URL through GIT_CONFIG_COUNT, GIT_CONFIG_KEY_n and GIT_CONFIG_VALUE_n, which are appended
// after the clean environment is made: it is on no command line, in no .git/config and in no
// file. The commands that cannot reach a remote, among them the commits whose hooks are the
// repository's own, do not have it in their environment.
//
// home is an empty directory the caller owns. wrap makes the runner of an environment; nil is
// gitx.ExplicitEnvRunner.
func gitRunner(home, cloneURL, pat string, wrap func(env []string) gitx.Runner) gitx.Runner {
	if wrap == nil {
		wrap = gitx.ExplicitEnvRunner
	}
	clean := gitx.HermeticEnv(tools.ReducedEnv(nil), home)
	credentialed := append(slices.Clone(clean),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http."+cloneURL+".extraHeader",
		"GIT_CONFIG_VALUE_0="+basicHeader(pat),
	)
	plain, carrying := wrap(clean), wrap(credentialed)
	return func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		if reachesRemote(argv) {
			return carrying(ctx, dir, argv, stdin...)
		}
		return plain(ctx, dir, argv, stdin...)
	}
}

// basicHeader is the header that hub's git server takes for the PAT.
func basicHeader(pat string) string {
	return "Authorization: Basic " + basicToken(pat)
}

// basicToken is the base64 of the credential.
func basicToken(pat string) string {
	return base64.StdEncoding.EncodeToString([]byte(gitUser + ":" + pat))
}

// remoteCommands are the git commands that talk to a remote.
var remoteCommands = []string{"clone", "fetch", "pull", "push", "ls-remote"}

// reachesRemote says whether the git command argv can reach a remote. The command is the first
// word that is not an option of git itself; one that cannot be found is not one that does.
func reachesRemote(argv []string) bool {
	if len(argv) < 2 || argv[0] != "git" {
		return false
	}
	args := argv[1:]
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace" || a == "--exec-path":
			i++ // the option takes the next word
		case strings.HasPrefix(a, "-"):
		default:
			return slices.Contains(remoteCommands, a)
		}
	}
	return false
}

// redact hides what must not be seen from text: the PAT and the header it travels in.
func redact(text, pat string) string {
	if pat == "" {
		return text
	}
	return strings.NewReplacer(pat, "***", basicToken(pat), "***").Replace(text)
}

// publishTries is how many times the branch is handed to hub in all, while hub is unavailable.
const publishTries = 3

// publishBackoff is how long to wait before the next try, after the given number of tries.
func publishBackoff(tries int) time.Duration { return time.Duration(tries) * time.Second }

// publishBranch is the hook that follows the push of a branch of the fix pipeline to hub's git
// server: it asks hub to push the branch on to the forge, before any pull request is asked for.
// A hub that is unavailable is asked again, up to publishTries times in all; any other answer is
// final, and so is the end of the attempt.
func (r *Runner) publishBranch(a *attempt) func(ctx context.Context, branch string) error {
	return func(ctx context.Context, branch string) error {
		var err error
		for tries := 1; ; tries++ {
			err = r.cfg.Hub.PushBranch(ctx, a.pat, a.req.Workspace, branch)
			var unavailable *hub.UnavailableError
			if err == nil || !errors.As(err, &unavailable) || tries >= publishTries || ctx.Err() != nil {
				return err
			}
			if err := r.wait(ctx, publishBackoff(tries)); err != nil {
				return err
			}
		}
	}
}

// wait waits for d, or for the end of ctx, whose error it then returns.
func (r *Runner) wait(ctx context.Context, d time.Duration) error {
	if r.sleep != nil {
		return r.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
