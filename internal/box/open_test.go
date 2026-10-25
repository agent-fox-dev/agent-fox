package box

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

func TestOpen_SyncsTheCheckoutInWithItsGitAndRecordsIt(t *testing.T) {
	dir := hostRepo(t, nil)
	testutil.Git(t, dir, "checkout", "-q", "-b", "feature/ünï")
	testutil.Commit(t, dir, "second")
	host := strings.TrimSpace(testutil.Git(t, dir, "rev-parse", "--short", "HEAD"))
	l := &ledger{}

	s, err := Open(context.Background(), Request{Provider: "local", HostDir: dir, RunID: runID, Tool: "fix", Placement: "box", Record: l.record, Warn: l.warn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if s.Provider() != "local" || s.HostDir() != dir || s.Tool() != "fix" || s.Network() != NetworkHost || len(s.Tools()) == 0 || s.Spec().Labels["af.run_id"] != runID {
		t.Errorf("session: provider %q, host %q, tool %q, network %q, %d tools, labels %v", s.Provider(), s.HostDir(), s.Tool(), s.Network(), len(s.Tools()), s.Spec().Labels)
	}
	if got := inBox(t, s, "test -d .git && git rev-parse --short HEAD && git rev-parse --abbrev-ref HEAD"); got != host+"\nfeature/ünï" {
		t.Errorf("the box's git says %q, want %s on feature/ünï", got, host)
	}
	if got := inBox(t, s, "pwd"); got != s.Root() || got == dir {
		t.Errorf("the box works in %q, want its own workspace %q", got, s.Root())
	}
	if got := l.entries(); !slices.Equal(got, []string{"box_open", "box_sync"}) {
		t.Errorf("ledger = %v, want box_open, box_sync", got)
	}
	opened := l.last(t, "box_open")
	if opened["provider"] != "local" || opened["image"] != "" || opened["network"] != NetworkHost || opened["placement"] != "box" {
		t.Errorf("box_open = %v", opened)
	}
	if got := l.last(t, "box_sync")["head"]; got != host {
		t.Errorf("box_sync head = %v, want %s", got, host)
	}
}

func TestOpen_GivesTheProviderWhatTheRequestAsksFor(t *testing.T) {
	dir := hostRepo(t, nil)
	f := &fake{}
	l := &ledger{}
	req := request("fake", dir, registry(f.entry()), l)
	req.Image, req.TTL, req.Network = "example.test/img:1", 15*time.Minute, NetworkOpen
	req.Env = map[string]string{"ANTHROPIC_API_KEY": "k"}
	req.Labels = map[string]string{"af.job": "job_1", "af.tool": "given"}

	s, err := Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	spec := f.spec
	wantLabels := map[string]string{"af.run_id": runID, "af.tool": "given", "af.job": "job_1"}
	if len(spec.Labels) != 3 || spec.Labels["af.run_id"] != runID || spec.Labels["af.tool"] != "given" || spec.Labels["af.job"] != "job_1" {
		t.Errorf("labels = %v, want %v", spec.Labels, wantLabels)
	}
	if spec.Image != req.Image || spec.TTL != req.TTL || len(spec.Env) != 1 || spec.Env["ANTHROPIC_API_KEY"] != "k" || spec.Network.Mode != sandbox.NetworkUnrestricted {
		t.Errorf("spec = %+v, want the image, TTL, environment and network of the request", spec)
	}
	if s.Image() != req.Image || s.Network() != NetworkOpen || l.last(t, "box_open")["image"] != req.Image {
		t.Errorf("image %q and network %q, box_open %v: want the request's", s.Image(), s.Network(), l.last(t, "box_open"))
	}
}

func TestOpen_RefusesWhatIsNotTheTopLevelOfACheckoutOfItsOwn(t *testing.T) {
	repo := hostRepo(t, nil)
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "wt")
	testutil.Git(t, repo, "worktree", "add", "-q", "-b", "feature", worktree)
	for _, tc := range []struct {
		name, provider, dir, says string
		category                  string
	}{
		{"an unknown provider, before the directory", "docker", "", "is not a box provider", toolio.CategoryUsage},
		{"a directory that is no work tree", "fake", t.TempDir(), "not inside a git work tree", toolio.CategoryUsage},
		{"a missing directory", "fake", filepath.Join(repo, "nowhere"), "not inside a git work tree", toolio.CategoryUsage},
		{"a directory inside the work tree", "fake", sub, "is not its top level, " + repo, toolio.CategoryUsage},
		{"a linked worktree, whose .git is elsewhere", "fake", worktree, "does not hold its own .git directory", toolio.CategoryUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{}

			s, err := Open(context.Background(), request(tc.provider, tc.dir, registry(f.entry()), &ledger{}))

			if s != nil || f.creates.Load() != 0 {
				t.Errorf("a session %v and %d boxes were made", s, f.creates.Load())
			}
			if got := asFailure(t, err); got.Category != tc.category || !strings.Contains(got.Message, tc.says) {
				t.Errorf("failure = %+v, want %s saying %q", got, tc.category, tc.says)
			}
		})
	}
}

func TestOpen_TakesALinkOrARelativePathToTheTopLevel(t *testing.T) {
	repo := hostRepo(t, nil)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	f := &fake{}
	if s := f.open(t, link, &ledger{}); s.HostDir() != repo {
		t.Errorf("through a link: HostDir = %q, want %q", s.HostDir(), repo)
	}
	t.Chdir(repo)
	if s := f.open(t, ".", &ledger{}); s.HostDir() != repo {
		t.Errorf("from .: HostDir = %q, want %q", s.HostDir(), repo)
	}
}

func TestOpen_AProviderThatCannotMakeTheBoxIsASandboxFailureThatIsNotRetried(t *testing.T) {
	dir := hostRepo(t, nil)
	given := &toolio.Failure{Category: toolio.CategoryConfig, Message: "the provider's own failure"}
	for _, tc := range []struct {
		name    string
		f       *fake
		explain func(Request, error) error
		want    string
	}{
		{"the provider cannot be made", &fake{makeErr: errors.New("no engine")}, nil, "the fake provider cannot be made: no engine"},
		{"it cannot create the box", &fake{createErr: errors.New("no capacity")}, nil, "the fake provider could not create the box: no capacity"},
		{"the entry explains it", &fake{createErr: errors.New("no capacity")}, func(_ Request, err error) error {
			return &toolio.Failure{Category: toolio.CategorySandbox, Message: "explained: " + err.Error()}
		}, "explained: no capacity"},
		{"it is a failure already", &fake{createErr: fmt.Errorf("wrapped: %w", given)}, nil, "the provider's own failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.f.entry()
			e.Explain = tc.explain
			l := &ledger{}

			s, err := Open(context.Background(), request("fake", dir, registry(e), l))

			f := asFailure(t, err)
			if s != nil || f.Retryable || f.Message != tc.want {
				t.Errorf("Open = %v, %+v, want no session and %q, not retryable", s, f, tc.want)
			}
			if tc.f.makeErr == nil && tc.f.providerCloses.Load() != 1 {
				t.Errorf("the provider that made no box was closed %d times, want once", tc.f.providerCloses.Load())
			}
			if len(l.entries()) != 0 {
				t.Errorf("ledger = %v, want nothing for a box that was never made", l.entries())
			}
		})
	}
}

func TestOpen_ABoxThatCannotBeMadeReadyIsClosedBeforeOpenReturns(t *testing.T) {
	dir := hostRepo(t, nil)
	for _, tc := range []struct {
		name      string
		f         *fake
		says      string
		retryable bool
	}{
		{"the sync fails", &fake{syncErr: errors.New("transfer broke")}, "could not be synced from " + dir + ": transfer broke", true},
		{"the tools cannot be had", &fake{toolsErr: errors.New("no agent")}, "could not give the box's tools: no agent", false},
		{"the close fails too", &fake{syncErr: errors.New("transfer broke"), closeErr: errors.New("the box would not go")}, "transfer broke", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &ledger{}

			s, err := Open(context.Background(), request("fake", dir, registry(tc.f.entry()), l))

			f := asFailure(t, err)
			if s != nil || f.Category != toolio.CategorySandbox || f.Retryable != tc.retryable || !strings.Contains(f.Message, tc.says) {
				t.Errorf("Open = %v, %+v, want sandbox saying %q, retryable %v", s, f, tc.says, tc.retryable)
			}
			if tc.f.creates.Load() != 1 || tc.f.closes.Load() != 1 || tc.f.providerCloses.Load() != 1 {
				t.Errorf("%d boxes made, %d closed, provider closed %d times, want 1 each", tc.f.creates.Load(), tc.f.closes.Load(), tc.f.providerCloses.Load())
			}
			if got := l.entries(); len(got) < 2 || got[0] != "box_open" || got[len(got)-1] != "box_close" {
				t.Errorf("ledger = %v, want box_open first and box_close last", got)
			}
			if warned := l.warned("box_close_failed"); (tc.f.closeErr != nil) != (len(warned) == 1 && strings.Contains(warned[0].Message, "would not go")) {
				t.Errorf("warnings = %+v, want box_close_failed only for a close that fails", l.warnings)
			}
		})
	}
}

func TestOpen_AnUnconfinedProviderRaisesBoxUnconfinedOnce(t *testing.T) {
	dir := hostRepo(t, nil)
	for _, unconfined := range []bool{true, false} {
		f := &fake{}
		e := f.entry()
		e.Unconfined = unconfined
		l := &ledger{}
		s := open(t, registry(e), "fake", dir, l)
		if err := s.Sync(context.Background()); err != nil {
			t.Fatal(err)
		}

		warned := l.warned("box_unconfined")
		if want := map[bool]int{true: 1, false: 0}[unconfined]; len(warned) != want || len(l.warnings) != want {
			t.Errorf("unconfined %v: warnings = %+v, want %d box_unconfined and nothing else", unconfined, l.warnings, want)
		}
		if unconfined && !strings.Contains(warned[0].Message, "no process or network isolation") {
			t.Errorf("box_unconfined says %q", warned[0].Message)
		}
	}
}

func TestOpen_ADefaultProviderThatFailsSaysHowToChooseAnotherAndNothingElseIsTried(t *testing.T) {
	dir := hostRepo(t, nil)
	f, other := &fake{makeErr: errors.New("no engine")}, &fake{}
	otherEntry := other.entry()
	otherEntry.Name = "other"
	r := registry(f.entry(), otherEntry)
	for _, defaulted := range []bool{true, false} {
		req := request("fake", dir, r, &ledger{})
		req.Defaulted = defaulted

		_, err := Open(context.Background(), req)

		got := asFailure(t, err)
		if defaulted && (got.FixHint == nil || got.FixHint.Flag != "--sandbox" || got.FixHint.Env != "AF_SANDBOX") {
			t.Errorf("a default provider: fix_hint = %+v, want --sandbox and AF_SANDBOX", got.FixHint)
		}
		if !defaulted && got.FixHint != nil {
			t.Errorf("a provider asked for: fix_hint = %+v, want none", got.FixHint)
		}
	}
	if other.creates.Load() != 0 {
		t.Error("another provider was tried")
	}
}

func TestDefaultHint_IsAddedToTheSandboxFailureOfADefaultProviderOnly(t *testing.T) {
	sandboxFailure := &toolio.Failure{Category: toolio.CategorySandbox, Message: "m", FixHint: &toolio.FixHint{Env: "AF_SANDBOX_IMAGE", Suggest: "build it"}}
	usage := &toolio.Failure{Category: toolio.CategoryUsage, Message: "m"}
	plain := errors.New("not a failure")

	got := asFailure(t, Request{Defaulted: true}.defaultHint(sandboxFailure))
	if *got.FixHint != (toolio.FixHint{Flag: "--sandbox", Env: "AF_SANDBOX", Suggest: "build it"}) || got.Message != "m" {
		t.Errorf("hint = %+v, want --sandbox and AF_SANDBOX with the suggestion kept", got.FixHint)
	}
	if sandboxFailure.FixHint.Env != "AF_SANDBOX_IMAGE" {
		t.Errorf("the failure given was changed: %+v", sandboxFailure.FixHint)
	}
	for name, err := range map[string]error{"a usage failure": usage, "an error": plain, "no error": nil} {
		if out := (Request{Defaulted: true}).defaultHint(err); out != err {
			t.Errorf("%s: defaultHint = %v, want it as it was", name, out)
		}
	}
	if out := (Request{}).defaultHint(sandboxFailure); out != error(sandboxFailure) {
		t.Errorf("a provider asked for: defaultHint = %v, want the failure as it was", out)
	}
}

func TestOpen_ALedgerThatCannotBeWrittenIsWarnedOnceAndDoesNotStopTheBox(t *testing.T) {
	l := &ledger{fail: errors.New("disk full")}
	s := (&fake{}).open(t, hostRepo(t, nil), l)

	if _, err := s.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if warned := l.warned("ledger_unwritable"); len(warned) != 1 || !strings.Contains(warned[0].Message, "box_open could not be written: disk full") {
		t.Errorf("warnings = %+v, want one ledger_unwritable for the first entry", l.warnings)
	}
}
