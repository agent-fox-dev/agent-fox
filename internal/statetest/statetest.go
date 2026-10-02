// Package statetest isolates test runs from the developer's real state
// directory. It is imported from TestMain in every package whose tests call
// App.Main, so that the events file (always written since spec 12) never
// lands under the real $HOME/.local/state/agent-fox.
//
// Usage in TestMain:
//
//	func TestMain(m *testing.M) {
//	    os.Exit(statetest.Run(m))
//	}
package statetest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Runner is the subset of *testing.M that Run needs.
type Runner interface {
	Run() int
}

// RunnerFunc adapts a plain function to the Runner interface.
type RunnerFunc func() int

// Run calls f().
func (f RunnerFunc) Run() int { return f() }

// Run sets XDG_STATE_HOME to a fresh temporary directory, calls m.Run(),
// removes the directory, and then compares the listing of the real default
// state directories against a listing taken before the run. If any new file
// appeared under runs/ or events/ of a real directory, Run returns a non-zero
// exit code and prints a message naming each new path.
func Run(m Runner) int {
	code, msg := runCheck(m)
	if msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	return code
}

// runCheck is the unexported variant that returns the exit code and a
// diagnostic message (empty when no stray files were found). Tests use it
// to drive the helper with fakes and inspect the message.
func runCheck(m Runner) (int, string) {
	// 1. Record the original XDG_STATE_HOME value.
	origXDG := os.Getenv("XDG_STATE_HOME")

	// 2. Compute the real directories to watch.
	home, homeErr := os.UserHomeDir()
	var dirs []string
	if homeErr == nil {
		dirs = realDirs(home, origXDG)
	}

	// 3. Snapshot the files under runs/ and events/ of each real directory.
	before := snapshot(dirs)

	// 4. Create a temp directory and set XDG_STATE_HOME to it.
	tmp, err := os.MkdirTemp("", "statetest-*")
	if err != nil {
		panic(fmt.Sprintf("statetest: cannot create temp dir: %v", err))
	}
	os.Setenv("XDG_STATE_HOME", tmp)

	// 5. Run the tests.
	code := m.Run()

	// 6. Remove the temp directory and restore the original env.
	os.RemoveAll(tmp)
	if origXDG != "" {
		os.Setenv("XDG_STATE_HOME", origXDG)
	} else {
		os.Unsetenv("XDG_STATE_HOME")
	}

	// 7. If no home directory could be resolved, skip the comparison.
	if homeErr != nil {
		return code, ""
	}

	// 8. Re-list and compare.
	after := snapshot(dirs)
	var newFiles []string
	for path := range after {
		if _, ok := before[path]; !ok {
			newFiles = append(newFiles, path)
		}
	}

	if len(newFiles) > 0 {
		var b strings.Builder
		b.WriteString("statetest: test run created files under the real state directory:\n")
		for _, f := range newFiles {
			fmt.Fprintf(&b, "  %s\n", f)
		}
		if code == 0 {
			code = 1
		}
		return code, b.String()
	}

	return code, ""
}

// realDirs computes the list of real state directories to watch. home is the
// value of os.UserHomeDir, and xdg is the value of XDG_STATE_HOME before the
// run (empty when unset).
func realDirs(home, xdg string) []string {
	dirs := []string{filepath.Join(home, ".local", "state", "agent-fox")}
	if xdg != "" {
		dirs = append(dirs, filepath.Join(xdg, "agent-fox"))
	}
	return dirs
}

// snapshot lists all files under runs/ and events/ of each directory in dirs.
// It returns a set of paths (relative to the directory) for comparison.
func snapshot(dirs []string) map[string]struct{} {
	files := make(map[string]struct{})
	for _, dir := range dirs {
		for _, sub := range []string{"runs", "events"} {
			base := filepath.Join(dir, sub)
			entries, err := os.ReadDir(base)
			if err != nil {
				continue // directory may not exist
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				files[filepath.Join(sub, e.Name())] = struct{}{}
			}
		}
	}
	return files
}
