package containers

import (
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"testing"
)

// read returns a file of the repository, given by its path from the root.
func read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// moduleBuildDir is the directory of the build stage that the source of af is copied
// into, which is the WORKDIR in force at the line `COPY . .`.
func moduleBuildDir(t *testing.T, containerfile string) string {
	t.Helper()
	dir := ""
	for _, line := range strings.Split(containerfile, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "WORKDIR "); ok {
			dir = strings.TrimSpace(rest)
		}
		if line == "COPY . ." {
			if dir == "" {
				t.Fatal("COPY . . comes before any WORKDIR")
			}
			return dir
		}
	}
	t.Fatal("the Containerfile never copies the source of af (COPY . .)")
	return ""
}

// TestTheToolsContainerfileBuildsAfAndInstallsClaudeCode_TS_10_22 verifies TS-10-22:
// the Containerfile of the tools image builds from the sandbox image, builds af from
// this repository with agentkit-go cloned where the replace directive expects it,
// installs Claude Code at a version, and ends in /workspace as 1001:0.
// Verifies: 10-REQ-4.4
func TestTheToolsContainerfileBuildsAfAndInstallsClaudeCode_TS_10_22(t *testing.T) {
	text := read(t, "containers/tools/Containerfile")

	for _, tc := range []struct{ name, pattern string }{
		{"the base image defaults to the sandbox image", `(?m)^ARG BASE_IMAGE=quay\.io/agentfox/sandbox:latest\s*$`},
		{"the run stage is built from the base image", `(?m)^FROM \$\{BASE_IMAGE\}\s*$`},
		{"the ref of agentkit-go is an argument", `(?m)^ARG AGENTKIT_REF(=\S*)?\s*$`},
		{"the version of Claude Code is an argument", `(?m)^ARG CLAUDE_CODE_VERSION(=\S*)?\s*$`},
		{"agentkit-go is cloned at the ref", `git clone[^\n]*\$\{AGENTKIT_REF\}`},
		{"Claude Code is installed at the version", `\$\{CLAUDE_CODE_VERSION\}`},
		{"af is built from the source", `(?s)go build.{0,300}?\./cmd/af`},
		{"af is in the image", `(?m)^COPY --from=build \S+ /usr/local/bin/af\s*$`},
	} {
		if !regexp.MustCompile(tc.pattern).MatchString(text) {
			t.Errorf("%s: no line matches %s", tc.name, tc.pattern)
		}
	}

	// The last WORKDIR and USER are the ones the image runs with.
	workdir, user := "", ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "WORKDIR "); ok {
			workdir = rest
		}
		if rest, ok := strings.CutPrefix(line, "USER "); ok {
			user = rest
		}
	}
	if workdir != "/workspace" || user != "1001:0" {
		t.Errorf("the image ends with WORKDIR %q and USER %q, want /workspace and 1001:0", workdir, user)
	}

	// agentkit-go goes where the replace directive of go.mod looks for it, from the
	// directory af is built in.
	replace := regexp.MustCompile(`(?m)^replace github\.com/agent-fox-dev/agentkit-go => (\S+)\s*$`).FindStringSubmatch(read(t, "go.mod"))
	if replace == nil {
		t.Fatal("go.mod has no replace directive for agentkit-go")
	}
	want := path.Join(moduleBuildDir(t, text), replace[1])
	if !regexp.MustCompile(`(?m)git clone[^\n]* ` + regexp.QuoteMeta(want) + `\s*(\\|$)`).MatchString(text) {
		t.Errorf("agentkit-go is not cloned into %s, where go.mod's replace (%s) looks for it from the build directory", want, replace[1])
	}
}

// makeDryRun runs make -n with the targets and variables, and returns what it would run.
func makeDryRun(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	cmd := exec.Command("make", append([]string{"-n", "-C", ".."}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestTheMakefileBuildsTheToolsImageAfterTheSandboxImage_TS_10_23 verifies TS-10-23:
// make build-tools-container tags the image with the version and with latest, and
// make build-containers builds the sandbox image before it.
// Verifies: 10-REQ-4.5
func TestTheMakefileBuildsTheToolsImageAfterTheSandboxImage_TS_10_23(t *testing.T) {
	vars := []string{"VERSION=9.9.9", "CONTAINER_REGISTRY=example.test/reg"}

	tools := makeDryRun(t, append([]string{"build-tools-container"}, vars...)...)

	for _, want := range []string{"-t example.test/reg/tools:9.9.9", "-t example.test/reg/tools:latest", "-f containers/tools/Containerfile"} {
		if !strings.Contains(tools, want) {
			t.Errorf("make -n build-tools-container does not print %q:\n%s", want, tools)
		}
	}
	if strings.Contains(tools, "containers/sandbox/Containerfile") {
		t.Errorf("make -n build-tools-container builds the sandbox image:\n%s", tools)
	}

	all := makeDryRun(t, append([]string{"build-containers"}, vars...)...)

	sandbox, toolsAt := strings.Index(all, "-f containers/sandbox/Containerfile"), strings.Index(all, "-f containers/tools/Containerfile")
	if sandbox < 0 || toolsAt < 0 || sandbox > toolsAt {
		t.Errorf("make -n build-containers does not build the sandbox image (at %d) before the tools image (at %d):\n%s", sandbox, toolsAt, all)
	}
}
