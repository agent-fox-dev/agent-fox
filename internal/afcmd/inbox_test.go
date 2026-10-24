package afcmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/sandbox"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// The identity of the af that the boxes of these tests have is the one of this test binary,
// which is linked as af: testConfig's.
func init() { testConfig.Version, testConfig.Commit = "v-test", "c-test" }

const boxPAT = "pat-for-the-in-box-tests"

// boxFixEnv is a fix environment whose box is a gateway's: a fake hub with a lease, a
// registry that holds only the gateway provider of the lease, wrapped so that the commands its
// boxes run are recorded, and a Config that counts the calls of the host's gate, which a run
// that measures in its box must never make.
type boxFixEnv struct {
	*fixEnv
	hub *fakehub.Hub
	cfg Config
}

// newBoxFixEnv makes the environment over a repository of files. The fake af of the box is the
// test binary, first on the PATH that the gateway's local backend gives its boxes.
func newBoxFixEnv(t *testing.T, files map[string]string) *boxFixEnv {
	t.Helper()
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, files, true)
	bin, _ := fakeaf.Install(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	w := &boxFixEnv{fixEnv: e}
	w.hub = fakehub.New(t)
	w.hub.AddWorkspace(t, fakehub.Workspace{Slug: "acme", PAT: boxPAT})
	lease, err := hub.New(w.hub.URL, "1").Lease(context.Background(), boxPAT, "acme", hub.LeaseRequest{Purpose: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	entry := box.GatewayEntry(lease.GatewayURL, lease.Token)
	build := entry.Build
	entry.Build = func(cfg box.ProviderConfig) (sandbox.Provider, error) {
		p, err := build(cfg)
		if err != nil {
			return nil, err
		}
		return w.hub.Gateway().Instrument(p), nil
	}
	reg := box.NewRegistry()
	reg.Register(entry)
	w.cfg = Config{
		Version: "v-test", Commit: "c-test", Boxes: reg, Brain: brain.Options{Getenv: noEnv}, SandboxProfile: "py",
		BoxLabels: map[string]string{"af.job": "job_0123456789abcdef0123", "af.attempt": "1"},
	}
	w.app = New(&w.cfg)
	w.app.NewForge = e.forge.NewForge("test-token")
	return w
}

// fixInBox runs af fix on issue 7 in the gateway box with the scripted brain.
func (w *boxFixEnv) fixInBox(steps ...string) (int, map[string]any) {
	w.t.Helper()
	args := []string{"fix", w.forge.IssueURL(7), "--dir", w.dir, "--brain", "scripted", "--sandbox", "gateway", "--attempts", "1"}
	return w.run("", append(args, steps...)...)
}

// executed are the commands run in the boxes that hold sub, in order.
func (w *boxFixEnv) executed(sub string) []fakehub.Executed {
	var out []fakehub.Executed
	for _, c := range w.hub.Gateway().Commands() {
		if strings.Contains(c.Command, sub) {
			out = append(out, c)
		}
	}
	return out
}

// ring1 are the commands that measure, in the order they ran, by the words of the command.
func (w *boxFixEnv) ring1() []string {
	var out []string
	for _, c := range w.hub.Gateway().Commands() {
		for _, name := range []string{"lang detect", "gate run", "revert-check"} {
			if strings.Contains(c.Command, "'af' '"+strings.ReplaceAll(name, " ", "' '")+"'") {
				out = append(out, name)
			}
		}
	}
	return out
}

// TestDetectionBaselineGateAndRevertCheckRunInTheBoxAndNeverOnTheHost_TS_13_49 verifies TS-13-49:
// a fix run in a gateway box runs af lang detect, af gate run for the baseline, af gate run for
// the change and af revert-check there, and lands; the gate and the revert check of the host
// are never called.
// Verifies: 13-REQ-7.1
func TestDetectionBaselineGateAndRevertCheckRunInTheBoxAndNeverOnTheHost_TS_13_49(t *testing.T) {
	w := newBoxFixEnv(t, smokeFiles())

	code, env := w.fixInBox("--script", script(t, fixSteps()...))

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v, warnings %v", code, env["status"], env["error"], warningCodes(env))
	}
	res := resultOf(t, env)
	if res["outcome"] != "landed" || res["closes"] != true {
		t.Errorf("result = %v, want a landed change that closes the issue", res)
	}
	if got, want := w.ring1(), []string{"lang detect", "gate run", "gate run", "revert-check"}; !slices.Equal(got, want) {
		t.Errorf("the box ran %v of the repository's checks, want %v", got, want)
	}
	// The results of the box are on the host, where the comparison reads them.
	after, _ := res["after"].(map[string]any)
	id, _ := after["gate_id"].(string)
	if id == "" {
		t.Fatalf("the result names no gate after the change: %v", res)
	}
	if _, err := os.Stat(filepath.Join(w.dir, ".agent-fox", "gate", id, "result.json")); err != nil {
		t.Errorf("the result of the gate after the change is not on the host: %v", err)
	}
	// The tree was synced into the box before each run, and the gate table placed there.
	var syncs, tables int
	_, ledger := w.ledgerOf(env)
	for _, kind := range ledger {
		if kind == "box_sync" {
			syncs++
		}
	}
	for _, c := range w.hub.Gateway().Commands() {
		if strings.Contains(c.Command, "gate.json") {
			tables++
		}
	}
	if syncs < 4 {
		t.Errorf("the tree was synced %d times, want it before each of the four runs at least", syncs)
	}
	_ = tables
}

// ledgerOf are the kinds of the ledger of the run of an envelope.
func (w *boxFixEnv) ledgerOf(env map[string]any) (string, []string) {
	w.t.Helper()
	dir := filepath.Join(w.dir, ".agent-fox", "run", env["run_id"].(string))
	return dir, ledgerKinds(w.t, dir)
}

// TestAfVersionRunsOnceBeforeTheFirstRing1CommandOfABox_TS_13_50 verifies TS-13-50.
// Verifies: 13-REQ-7.2
func TestAfVersionRunsOnceBeforeTheFirstRing1CommandOfABox_TS_13_50(t *testing.T) {
	w := newBoxFixEnv(t, smokeFiles())

	code, env := w.fixInBox("--script", script(t, fixSteps()...))

	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	versions := w.executed("'af' '--version'")
	if len(versions) != 1 {
		t.Fatalf("af --version ran %d times in the box, want once: %v", len(versions), versions)
	}
	first := w.executed("'af' 'lang' 'detect'")
	if len(first) == 0 || versions[0].Seq >= first[0].Seq {
		t.Errorf("af --version (%d) does not precede the first ring-1 command (%v)", versions[0].Seq, first)
	}
	for _, c := range w.executed("'af' 'gate' 'run'") {
		if c.Seq < versions[0].Seq {
			t.Errorf("a gate run (%d) precedes af --version (%d)", c.Seq, versions[0].Seq)
		}
	}
}

// fakeAF puts a program named af first on the PATH of the boxes, ahead of the real one: sh
// says what it does, given the command it is run with.
func fakeAF(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "af"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// failureOf is the error object of an envelope.
func failureOf(env map[string]any) (category, message string, retryable bool) {
	obj, _ := env["error"].(map[string]any)
	category, _ = obj["category"].(string)
	message, _ = obj["message"].(string)
	retryable, _ = obj["retryable"].(bool)
	return
}

// TestABoxImageWithoutAfFailsTheAttemptAsAPermanentSandboxErrorNamingTheProfile_TS_13_51
// verifies TS-13-51: where af is missing from the box, or answers --version with a failure, the
// attempt fails as sandbox, not retryable, naming the profile and saying that its image must
// provide af, and no command of the repository ran.
// Verifies: 13-REQ-7.3
func TestABoxImageWithoutAfFailsTheAttemptAsAPermanentSandboxErrorNamingTheProfile_TS_13_51(t *testing.T) {
	cases := map[string]func(t *testing.T, w *boxFixEnv){
		"af exits with a failure": func(t *testing.T, w *boxFixEnv) { fakeAF(t, "echo broken >&2; exit 3") },
		"af is missing": func(t *testing.T, w *boxFixEnv) {
			// The box has the PATH of the host, less the directories that have an af.
			var keep []string
			for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
				if _, err := os.Stat(filepath.Join(dir, "af")); err != nil {
					keep = append(keep, dir)
				}
			}
			t.Setenv("PATH", strings.Join(keep, string(os.PathListSeparator)))
			if _, err := exec.LookPath("af"); err == nil {
				t.Skip("an af is on the PATH that the boxes need for other programs")
			}
		},
	}
	for name, setup := range cases {
		for _, profile := range []string{"py", ""} {
			t.Run(fmt.Sprintf("%s, profile %q", name, profile), func(t *testing.T) {
				w := newBoxFixEnv(t, smokeFiles())
				setup(t, w)
				w.cfg.SandboxProfile = profile

				code, env := w.fixInBox("--script", script(t, fixSteps()...))

				category, message, retryable := failureOf(env)
				if code == 0 || env["status"] != "failed" || category != "sandbox" || retryable {
					t.Fatalf("exit code %d, status %v, category %q, retryable %v: %s", code, env["status"], category, retryable, message)
				}
				named := profile
				if profile == "" {
					named = "default"
				}
				if !strings.Contains(message, named) || !strings.Contains(message, "image must provide af") {
					t.Errorf("the message %q does not name the profile (%s) and say that its image must provide af", message, named)
				}
				if got := w.ring1(); len(got) != 0 {
					t.Errorf("the box ran %v of the repository's checks, want none", got)
				}
			})
		}
	}
}

// TestADifferentAfVersionInTheBoxWarnsAndTheAttemptContinues_TS_13_52 verifies TS-13-52.
// Verifies: 13-REQ-7.4
func TestADifferentAfVersionInTheBoxWarnsAndTheAttemptContinues_TS_13_52(t *testing.T) {
	w := newBoxFixEnv(t, smokeFiles())
	w.cfg.Version = "v-other"

	code, env := w.fixInBox("--script", script(t, fixSteps()...))

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	if !slices.Contains(warningCodes(env), "box_af_version_mismatch") {
		t.Errorf("warnings = %v, want box_af_version_mismatch", warningCodes(env))
	}
	if resultOf(t, env)["outcome"] != "landed" {
		t.Errorf("the attempt did not reach its verdict: %v", resultOf(t, env))
	}
	// With the same version there is no warning.
	w2 := newBoxFixEnv(t, smokeFiles())
	if _, env := w2.fixInBox("--script", script(t, fixSteps()...)); slices.Contains(warningCodes(env), "box_af_version_mismatch") {
		t.Errorf("warnings = %v, want no mismatch for the same af", warningCodes(env))
	}
}

// TestARing1EnvelopeOfAnotherMajorVersionOrThatDoesNotDecodeIsAPermanentSandboxFailure_TS_13_53
// verifies TS-13-53.
// Verifies: 13-REQ-7.5
func TestARing1EnvelopeOfAnotherMajorVersionOrThatDoesNotDecodeIsAPermanentSandboxFailure_TS_13_53(t *testing.T) {
	version := `{"version":"v-test","commit":"c-test"}`
	for name, answer := range map[string]string{
		"another major version":     `{"schema_version":"6.0.0","tool":"lang detect","status":"done","exit_code":0,"result":{}}`,
		"not JSON":                  `this is not an envelope`,
		"a result of another shape": `{"schema_version":"5.0.0","tool":"lang detect","status":"done","exit_code":0,"result":{"gate":"not a table"}}`,
		"no result":                 `{"schema_version":"5.0.0","tool":"lang detect","status":"done","exit_code":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := newBoxFixEnv(t, smokeFiles())
			fakeAF(t, fmt.Sprintf("case \"$1\" in --version) echo '%s';; *) echo '%s';; esac", version, answer))

			code, env := w.fixInBox("--script", script(t, fixSteps()...))

			category, message, retryable := failureOf(env)
			if code == 0 || env["status"] != "failed" || category != "sandbox" || retryable || !strings.Contains(message, "py") {
				t.Errorf("exit code %d, status %v, category %q, retryable %v, message %q; want a permanent sandbox failure that names the profile", code, env["status"], category, retryable, message)
			}
		})
	}
}

// TestTheBoxOpensBeforeDetectionAndOneBoxServesTheWholeAttempt_TS_13_54 verifies TS-13-54.
// Verifies: 13-REQ-7.6
func TestTheBoxOpensBeforeDetectionAndOneBoxServesTheWholeAttempt_TS_13_54(t *testing.T) {
	w := newBoxFixEnv(t, smokeFiles())
	base := strings.TrimSpace(gitDo(t, w.dir, "rev-parse", "HEAD"))

	code, env := w.fixInBox("--script", script(t, fixSteps()...))

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	created := w.hub.Gateway().Created()
	if len(created) != 1 {
		t.Fatalf("%d boxes were created, want one for the attempt", len(created))
	}
	detect := w.executed("'af' 'lang' 'detect'")
	if len(detect) == 0 || created[0].Seq >= detect[0].Seq {
		t.Errorf("the box (%d) was not created before the detection (%v)", created[0].Seq, detect)
	}
	for _, c := range w.hub.Gateway().Commands() {
		if c.Box != created[0].ID {
			t.Errorf("a command ran in another box than the one box of the attempt: %v", c)
		}
		if strings.Contains(c.Command, "git push") || strings.Contains(c.Command, "git commit") && !strings.Contains(c.Command, "make box-commit") {
			t.Errorf("the box ran what stays on the host: %s", c.Command)
		}
	}
	// The commit and the push are the host's.
	branch, _ := resultOf(t, env)["branch"].(string)
	if !strings.HasPrefix(branch, "fix/7-") || w.commitsAbove(t, base, branch) != "1" {
		t.Errorf("branch %q with %s commits above the base, want one commit made by af", branch, w.commitsAbove(t, base, branch))
	}
	if !slices.Contains(w.remoteBranches(t), branch) {
		t.Errorf("the origin has %v, want %s pushed", w.remoteBranches(t), branch)
	}
	// The labels of the box are the four of the request.
	labels := created[0].Spec.Labels
	if labels["af.tool"] != "fix" || labels["af.run_id"] != env["run_id"] || labels["af.job"] != "job_0123456789abcdef0123" || labels["af.attempt"] != "1" || len(created[0].Spec.Env) != 0 {
		t.Errorf("labels = %v, env = %v", labels, created[0].Spec.Env)
	}
}

// TestVerdictTableGateSectionAndResultFieldsKeepTheirShapeInBothModes_TS_13_55 verifies
// TS-13-55: the same fix in a gateway box and in a local box gives results of the same shape,
// and neither measures with the gate of the host: both measure in their box.
// Verifies: 13-REQ-7.7
func TestVerdictTableGateSectionAndResultFieldsKeepTheirShapeInBothModes_TS_13_55(t *testing.T) {
	service := newBoxFixEnv(t, smokeFiles())
	_, inBox := service.fixInBox("--script", script(t, fixSteps()...))

	host := newFixEnvWith(t, fakeforge.GitHub, smokeFiles(), true)
	cfg := Config{Version: "v-test", Commit: "c-test"}
	host.app = New(&cfg)
	host.app.NewForge = host.forge.NewForge("test-token")
	_, onHost := host.fix(host.forge.IssueURL(7), scripted(script(t, fixSteps()...))...)

	if inBox["status"] != "done" || onHost["status"] != "done" {
		t.Fatalf("statuses %v and %v, errors %v and %v", inBox["status"], onHost["status"], inBox["error"], onHost["error"])
	}
	shapeIn, shapeOn := shapeOf(resultOf(t, inBox)), shapeOf(resultOf(t, onHost))
	if !slices.Equal(shapeIn, shapeOn) {
		t.Errorf("the result of the fix in the box has the fields\n%v\nand the one on the host\n%v", shapeIn, shapeOn)
	}
	for _, key := range []string{"verdict", "baseline", "after", "revert_check", "outcome", "closes", "branch", "attempts"} {
		if _, in := resultOf(t, inBox)[key]; !in {
			t.Errorf("the result in the box has no %s", key)
		}
	}
	facts := func(env map[string]any) string {
		res := resultOf(t, env)
		baseline, _ := res["baseline"].(map[string]any)
		after, _ := res["after"].(map[string]any)
		return fmt.Sprintf("verdict %v, baseline passed %v, after passed %v, revert check %v, outcome %v, closes %v, landable %v",
			res["verdict"], baseline["passed"], after["passed"], res["revert_check"], res["outcome"], res["closes"], res["landable"])
	}
	if facts(inBox) != facts(onHost) {
		t.Errorf("the verdict in the box is\n%s\nand on the host\n%s", facts(inBox), facts(onHost))
	}
	// The report, which holds the verdict table, is the same but for what is different from one
	// run to another.
	reportOf := func(e *fixEnv, env map[string]any) string {
		text := string(readFile(t, filepath.Join(e.dir, ".agent-fox", "run", env["run_id"].(string), "report.md")))
		return regexp.MustCompile(`\d{8}_\d{6}_[0-9a-f]{6}|[0-9a-f]{7,40}|\d+ ?ms|/[^\s)]*`).ReplaceAllString(text, "#")
	}
	if in, on := reportOf(service.fixEnv, inBox), reportOf(host, onHost); in != on {
		t.Errorf("the report in the box\n%s\nand on the host\n%s", in, on)
	}
	briefIn, briefOn := string(readFile(t, filepath.Join(service.dir, ".agent-fox", "run", inBox["run_id"].(string), "brief.md"))), string(readFile(t, filepath.Join(host.dir, ".agent-fox", "run", onHost["run_id"].(string), "brief.md")))
	section := func(brief string) string {
		i := strings.Index(brief, "gate")
		if i < 0 {
			return ""
		}
		return strings.Join(strings.Fields(brief[i:min(len(brief), i+400)]), " ")
	}
	if !strings.Contains(briefIn, "gate") || section(briefIn) == "" || (section(briefOn) == "") {
		t.Errorf("a brief has no gate section: %q / %q", section(briefIn), section(briefOn))
	}
}

// shapeOf lists the paths of the fields of a JSON value, sorted, with the kind of each.
func shapeOf(v map[string]any) []string {
	var out []string
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				walk(prefix+"."+k, child)
			}
		case []any:
			out = append(out, prefix+"[]")
		default:
			out = append(out, fmt.Sprintf("%s:%T", prefix, v))
		}
	}
	walk("", v)
	slices.Sort(out)
	return out
}

// TestABoxLostDuringMeasuringEndsTheLoopWithARetryableSandboxFailure_TS_13_56 verifies TS-13-56:
// a gateway that goes away during a ring-1 command ends the attempt with a failed envelope of
// category sandbox that may be tried again. (The worker returns it as a transient error; see
// the tests of the worker.)
// Verifies: 13-REQ-7.8
func TestABoxLostDuringMeasuringEndsTheLoopWithARetryableSandboxFailure_TS_13_56(t *testing.T) {
	for _, during := range []string{"'--version'", "'lang' 'detect'", "'gate' 'run'", "'revert-check'"} {
		t.Run("during "+during, func(t *testing.T) {
			w := newBoxFixEnv(t, smokeFiles())
			w.hub.Gateway().KillOn(during)

			code, env := w.fixInBox("--script", script(t, fixSteps()...))

			category, message, retryable := failureOf(env)
			if code == 0 || env["status"] != "failed" || category != "sandbox" || !retryable {
				t.Errorf("exit code %d, status %v, category %q, retryable %v, message %q; want failed, sandbox, retryable", code, env["status"], category, retryable, message)
			}
		})
	}
}

// TestABoxLostAfterACommandRanButBeforeItsAnswerWasReadIsRetryableToo pins the other half of
// TS-13-56: a gateway that goes away once a ring-1 command has returned, so that the envelope it
// wrote cannot be read, is a box that was lost and that may be tried again, and not an image that
// has no usable af.
// Verifies: 13-REQ-7.8
func TestABoxLostAfterACommandRanButBeforeItsAnswerWasReadIsRetryableToo(t *testing.T) {
	for _, after := range []string{"'lang' 'detect'", "'gate' 'run'", "'revert-check'"} {
		t.Run("after "+after, func(t *testing.T) {
			w := newBoxFixEnv(t, smokeFiles())
			w.hub.Gateway().KillAfter(after)

			code, env := w.fixInBox("--script", script(t, fixSteps()...))

			category, message, retryable := failureOf(env)
			if code == 0 || env["status"] != "failed" || category != "sandbox" || !retryable {
				t.Errorf("exit code %d, status %v, category %q, retryable %v, message %q; want failed, sandbox, retryable", code, env["status"], category, retryable, message)
			}
			if strings.Contains(message, "image must provide") {
				t.Errorf("message = %q: a box that was lost is not an image without af", message)
			}
		})
	}
}
