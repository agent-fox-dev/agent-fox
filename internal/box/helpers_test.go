package box

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

const runID = "20261020_120000_abcdef"

// hostRepo is a checkout on main whose first commit holds the files (file0.txt
// alone when there are none), with its links resolved and an identity in its
// own configuration: the box has a HOME of its own, and a commit made in it
// needs the one in .git/config.
func hostRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	testutil.Require(t, "git", "sh")
	if files == nil {
		files = map[string]string{"file0.txt": "content 0\n"}
	}
	dir, err := filepath.EvalSymlinks(testutil.Repo(t, files))
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "config", "user.name", "Test")
	testutil.Git(t, dir, "config", "user.email", "test@example.com")
	testutil.Git(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

// plainDir is a directory with the files, and no repository.
func plainDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		testutil.WriteFile(t, dir, name, content)
	}
	return dir
}

// ledger takes what a session records and the warnings it raises. When fail
// is set, every record fails with it.
type ledger struct {
	mu       sync.Mutex
	kinds    []string
	data     []map[string]any
	warnings []toolio.Warning
	fail     error
}

func (l *ledger) record(kind string, data any) error {
	if l.fail != nil {
		return l.fail
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kinds = append(l.kinds, kind)
	l.data = append(l.data, m)
	return nil
}

func (l *ledger) warn(code, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnings = append(l.warnings, toolio.Warning{Code: code, Message: message})
}

// entries are the kinds recorded, in order.
func (l *ledger) entries() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.kinds)
}

// warned are the warnings of the code.
func (l *ledger) warned(code string) []toolio.Warning {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []toolio.Warning
	for _, w := range l.warnings {
		if w.Code == code {
			out = append(out, w)
		}
	}
	return out
}

// last is the data of the last entry of the kind.
func (l *ledger) last(t *testing.T, kind string) map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.kinds) - 1; i >= 0; i-- {
		if l.kinds[i] == kind {
			return l.data[i]
		}
	}
	t.Fatalf("the ledger has no %s entry: %v", kind, l.kinds)
	return nil
}

// request is a request for a box of the provider over dir that notes into l.
func request(provider, dir string, r *Registry, l *ledger) Request {
	return Request{Provider: provider, HostDir: dir, RunID: runID, Tool: "fix", Record: l.record, Warn: l.warn, Registry: r}
}

// open opens a box of the provider over dir, and closes it when the test ends.
func open(t *testing.T, r *Registry, provider, dir string, l *ledger) *Session {
	t.Helper()
	s, err := Open(context.Background(), request(provider, dir, r, l))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// openLocal opens a box of the default registry's local provider over dir.
func openLocal(t *testing.T, dir string, l *ledger) *Session {
	t.Helper()
	return open(t, nil, "local", dir, l)
}

// openHere opens a session over dir in place, and closes it when the test ends.
func openHere(t *testing.T, dir string, l *ledger) *Session {
	t.Helper()
	s, err := Here(context.Background(), Request{HostDir: dir, RunID: runID, Tool: "triage", Placement: "box", Record: l.record, Warn: l.warn})
	if err != nil {
		t.Fatalf("Here: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// callTool runs a tool of the box as a brain does.
func callTool(t *testing.T, s *Session, name string, args map[string]any) core.ToolResult {
	t.Helper()
	for _, tool := range s.Tools() {
		if tool.Name == name {
			return tool.Execute(context.Background(), testutil.JSON(t, args))
		}
	}
	t.Fatalf("the box has no tool %q", name)
	return core.ToolResult{}
}

// inBox runs a shell command in the box and returns what it printed, trimmed.
func inBox(t *testing.T, s *Session, command string) string {
	t.Helper()
	r := callTool(t, s, "execute", map[string]any{"command": command})
	if !r.OK {
		t.Fatalf("%s in the box: %s %s", command, r.Error, r.Detail)
	}
	return strings.TrimSpace(r.LLMText())
}

// stubTool replaces a tool of the session with one that answers what answer
// says, and returns the arguments it was asked with.
func stubTool(s *Session, name string, answer func(args map[string]any) core.ToolResult) *[]map[string]any {
	var asked []map[string]any
	for i, tool := range s.tools {
		if tool.Name == name {
			tool.Execute = func(_ context.Context, raw json.RawMessage) core.ToolResult {
				var args map[string]any
				_ = json.Unmarshal(raw, &args)
				asked = append(asked, args)
				return answer(args)
			}
			s.tools[i] = tool
		}
	}
	return &asked
}

// answer is a stub's answer that is always the result.
func answer(r core.ToolResult) func(map[string]any) core.ToolResult {
	return func(map[string]any) core.ToolResult { return r }
}

// asFailure is err as a *toolio.Failure.
func asFailure(t *testing.T, err error) *toolio.Failure {
	t.Helper()
	var f *toolio.Failure
	if !errors.As(err, &f) {
		t.Fatalf("error = %v (%T), want a *toolio.Failure", err, err)
	}
	return f
}

// registry is a registry of the entries.
func registry(entries ...Entry) *Registry {
	r := NewRegistry()
	for _, e := range entries {
		r.Register(e)
	}
	return r
}

// fake is the local provider with faults put in, and what it was asked. The
// faults are read when each call is made, so a test may set one on an open box.
type fake struct {
	makeErr, createErr, syncErr, toolsErr, exportErr, closeErr error
	// exportGit makes an export carry a .git, as no provider may.
	exportGit bool
	// afterExport is called once the box has exported, before Export returns.
	afterExport func()

	creates, closes, providerCloses atomic.Int32

	mu         sync.Mutex
	spec       sandbox.Spec
	exports    []string
	closeCalls []closeCall
}

// closeCall is the state of the context a box was closed with, at the close.
type closeCall struct {
	err         error
	deadline    time.Time
	hasDeadline bool
}

// entry is the fake as the unconfined provider "fake".
func (f *fake) entry() Entry {
	return Entry{Name: "fake", Unconfined: true, Build: func(ProviderConfig) (sandbox.Provider, error) {
		if f.makeErr != nil {
			return nil, f.makeErr
		}
		return fakeProvider{Provider: localEntry().New(), f: f}, nil
	}}
}

// open opens a box of the fake over dir.
func (f *fake) open(t *testing.T, dir string, l *ledger) *Session {
	t.Helper()
	return open(t, registry(f.entry()), "fake", dir, l)
}

func (f *fake) exported() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.exports)
}

type fakeProvider struct {
	sandbox.Provider
	f *fake
}

func (p fakeProvider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	p.f.mu.Lock()
	p.f.spec = spec
	p.f.mu.Unlock()
	if p.f.createErr != nil {
		return nil, p.f.createErr
	}
	spec.Image = "" // the local provider runs no image, and a box of some providers says none
	sb, err := p.Provider.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	p.f.creates.Add(1)
	return &fakeSandbox{Sandbox: sb, f: p.f}, nil
}

// Close makes the provider one that holds something, as a gateway's does.
func (p fakeProvider) Close() error { p.f.providerCloses.Add(1); return nil }

type fakeSandbox struct {
	sandbox.Sandbox
	f *fake
}

func (s *fakeSandbox) Sync(ctx context.Context, src sandbox.Source) (sandbox.SyncResult, error) {
	if s.f.syncErr != nil {
		return sandbox.SyncResult{}, s.f.syncErr
	}
	return s.Sandbox.Sync(ctx, src)
}

func (s *fakeSandbox) Tools(ctx context.Context, opts sandbox.ToolOptions) ([]core.Tool, error) {
	if s.f.toolsErr != nil {
		return nil, s.f.toolsErr
	}
	return s.Sandbox.Tools(ctx, opts)
}

func (s *fakeSandbox) Export(ctx context.Context, path string, dst sandbox.Destination) error {
	if s.f.exportErr != nil {
		return s.f.exportErr
	}
	err := s.Sandbox.Export(ctx, path, dst)
	if out, ok := dst.(sandbox.LocalDir); ok {
		s.f.mu.Lock()
		s.f.exports = append(s.f.exports, out.Path)
		s.f.mu.Unlock()
		if s.f.exportGit {
			if merr := os.MkdirAll(filepath.Join(out.Path, ".git"), 0o755); merr != nil {
				return merr
			}
		}
	}
	if s.f.afterExport != nil {
		s.f.afterExport()
	}
	return err
}

func (s *fakeSandbox) Close(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	s.f.mu.Lock()
	s.f.closeCalls = append(s.f.closeCalls, closeCall{err: ctx.Err(), deadline: deadline, hasDeadline: ok})
	s.f.mu.Unlock()
	s.f.closes.Add(1)
	err := s.Sandbox.Close(ctx)
	if s.f.closeErr != nil {
		return s.f.closeErr
	}
	return err
}
