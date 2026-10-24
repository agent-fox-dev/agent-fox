package box

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

// closeTimeout bounds every close of a sandbox, on a context detached from the
// caller's: the close of a session, and the close of a sandbox that was set up
// and could not be used.
const closeTimeout = 30 * time.Second

// The names of the hands' tools that the session itself uses, as the host's
// own hands and not as a brain's: they are not asked of the gate.
const (
	toolExecute   = "execute"
	toolWriteFile = "write_file"
	toolListFiles = "list_files"
)

// Session is one held box: a sandbox with one workspace, and the host
// directory it was synced from. The host directory is never written.
type Session struct {
	req      Request
	sb       sandbox.Sandbox
	tools    []core.Tool
	provider string
	hostDir  string
	network  string
	image    string

	// byName, supplies and remote are the flags of the entry the box was opened with, and
	// closer is the provider, closed once the box is.
	byName, supplies, remote bool
	closer                   io.Closer

	// probed are the programs a process runner has found in the box.
	probeMu sync.Mutex
	probed  map[string]bool

	ledgerWarned sync.Once

	// stream serialises the transfers: the box takes one stream at a time, and
	// a brain's tools may ask for several files together.
	stream sync.Mutex

	// inPlace says the session is over a directory where it is (Here): there is
	// nothing to sync, and no state of the directory is touched.
	inPlace bool

	mu      sync.Mutex
	closed  bool
	exports []string // the export directories Close removes
	lost    error    // set when a tool of the box found the sandbox gone, see Lost

}

// Provider is the name of the provider.
func (s *Session) Provider() string { return s.provider }

// Spec is the spec the box was created with, as the provider took it.
func (s *Session) Spec() sandbox.Spec { return s.sb.Spec() }

// Tools is the box's tool set, for brains: one proxy for each of the hands'
// built-in tools, whose every call runs inside the box. A call whose result says
// the sandbox is gone is noted: see Lost.
func (s *Session) Tools() []core.Tool {
	out := make([]core.Tool, len(s.tools))
	for i, t := range s.tools {
		out[i] = s.watch(t)
	}
	return out
}

// watch is the tool, whose results are watched for a sandbox that is gone.
func (s *Session) watch(t core.Tool) core.Tool {
	run := t.Execute
	t.Execute = func(ctx context.Context, raw json.RawMessage) core.ToolResult {
		r := run(ctx, raw)
		if resultGone(r) {
			s.lose(fmt.Errorf("%s: %s", r.Error, r.Detail))
		}
		return r
	}
	return t
}

// lose notes that the sandbox is gone, with what the box said, the first time.
func (s *Session) lose(cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lost == nil {
		s.lost = gone(cause)
	}
}

// goneIf is the error marked as ErrUnavailable when the cause says the sandbox is gone, and the loss is noted in the
// session: a box operation that is not a tool call, a sync or an export, notes it as a tool call does.
func (s *Session) goneIf(cause bool, err error) error {
	if cause {
		s.lose(err)
	}
	return goneIf(cause, err)
}

// Lost is the error that says the box found its sandbox gone or out of reach: a tool of the box, one of a brain's
// or one of the host's own, was told so, or a sync or an export of the host failed for it. It is nil while none
// was, and matches ErrUnavailable. The operation that found it returns its error to its caller all the same.
func (s *Session) Lost() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lost
}

// ByName says the programs of the box are called by name and found on the PATH of its image.
func (s *Session) ByName() bool { return s.byName }

// SuppliesCredentials says the box has the credentials of its brain in an environment of its
// own, which a process is told only the names of.
func (s *Session) SuppliesCredentials() bool { return s.supplies }

// Probed says whether the program was probed in the box already, by whoever asked first: the
// box is probed once for a program, whichever process runner asks.
func (s *Session) Probed(program string) bool {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	return s.probed[program]
}

// MarkProbed notes that the program was probed in the box.
func (s *Session) MarkProbed(program string) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if s.probed == nil {
		s.probed = map[string]bool{}
	}
	s.probed[program] = true
}

// Network is what the box's tools may reach: none, open, or host for a provider
// that enforces nothing.
func (s *Session) Network() string { return s.network }

// Tool is the agent the box was opened for, af.tool in its labels: triage or fix.
func (s *Session) Tool() string { return s.req.Tool }

// Image is the image the box was made from, as the box_open entry of the ledger says
// it; empty for a provider that has none.
func (s *Session) Image() string { return s.image }

// Root is the root of the tree as a process in the box sees it: the directory of a
// session in place (Here), and the workspace of the sandbox for the others.
func (s *Session) Root() string { return s.sb.Spec().Workdir }

// HostDir is the host checkout the box was synced from, the top level of its
// work tree with its links resolved.
func (s *Session) HostDir() string { return s.hostDir }

// Sync syncs the host directory into the box again, and records box_sync. It
// does not raise box_unconfined again.
//
// The box then holds what the host holds: a file that the box has and the host
// does not is removed, a file that the box changed is put back, and git's own
// state (HEAD, the branches, the index) is the host's. The transfer takes a
// file for unchanged when its size and its modification time, to the second,
// are the same. That would keep the box on a branch that the host has moved on
// within the second of the last sync, since a branch file is as long as a hash
// is, and so git's own bookkeeping is made new first: everything at the top of
// .git but the objects, which are named by their content and cannot be stale,
// is removed and sent again. A file of the tree that the box changed to another
// content of the same length within the second of the host's modification of it
// is still not put back; a checkout does not come to that outside a test.
func (s *Session) Sync(ctx context.Context) error {
	if s.inPlace {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: "box: Sync on a session over a directory in place: there is nothing to sync"}
	}
	if err := s.refreshGit(ctx); err != nil {
		return err
	}
	if err := s.sync(ctx); err != nil {
		return err
	}
	return s.scrubGit(ctx)
}

// refreshGit removes git's own state from the box, for the sync to send again.
func (s *Session) refreshGit(ctx context.Context) error {
	const command = "[ -d .git ] && find .git -mindepth 1 -maxdepth 1 ! -name objects -exec rm -rf {} + ; true"
	return s.prepare(ctx, "a sync", command)
}

// scrubGit takes what could carry a credential out of the box's git
// configuration: the URLs of the remotes (which may hold a token), the extra
// headers, the credential helpers and the rewrites of URLs. The box has no
// remote to reach; git and the credentials stay on the host.
func (s *Session) scrubGit(ctx context.Context) error {
	const command = `[ -d .git ] && git config --local --name-only --get-regexp '^(remote\..*\.(url|pushurl)|url\..*|http\..*|credential\..*|core\.(sshcommand|askpass|gitproxy))$' | while IFS= read -r key; do git config --local --unset-all "$key"; done ; true`
	return s.prepare(ctx, "its git configuration to be cleaned", command)
}

// prepare runs a shell command in the box as part of making it ready, and what
// it is for names the step in the failure.
func (s *Session) prepare(ctx context.Context, what, command string) error {
	r, err := s.call(ctx, toolExecute, map[string]any{"command": command})
	lost := err == nil && resultGone(r)
	if err == nil && !r.OK {
		err = fmt.Errorf("%s: %s", r.Error, r.Detail)
	}
	if err != nil {
		return s.goneIf(lost, &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: fmt.Sprintf("the %s box could not be prepared for %s: %v", s.provider, what, err)})
	}
	return nil
}

// sync syncs the host directory in with its .git, sends the tracked files that
// an ignore rule hides from the sync, and records box_sync with the host's
// short HEAD, or "" for a repository with no commit.
func (s *Session) sync(ctx context.Context) error {
	s.stream.Lock()
	err := s.syncLocked(ctx)
	s.stream.Unlock()
	if err != nil {
		return s.goneIf(isGone(err), &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: fmt.Sprintf("the %s box could not be synced from %s: %v", s.provider, s.hostDir, err)})
	}
	head, err := gitx.New(s.hostDir, nil).Head(ctx)
	if err != nil {
		head = ""
	}
	s.record("box_sync", map[string]any{"head": head})
	return nil
}

func (s *Session) syncLocked(ctx context.Context) error {
	if _, err := s.sb.Sync(ctx, syncSource(s.hostDir)); err != nil {
		return err
	}
	// The sync leaves out what the ignore rules match, tracked or not. A tracked
	// file that a rule matches is part of the checkout all the same: without it
	// git in the box would see it deleted.
	rels, err := gitx.New(s.hostDir, nil).TrackedIgnored(ctx)
	if err != nil {
		return err
	}
	if len(rels) == 0 {
		return nil
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarFiles(pw, s.hostDir, rels)) }()
	_, err = s.sb.Sync(ctx, sandbox.Tar{R: pr, Path: "."})
	pr.CloseWithError(err)
	return err
}

// tarFiles writes the regular files rels of dir to w as a tar stream. A link,
// or a file that is gone, is left out: a link could lead out of the tree.
func tarFiles(w io.Writer, dir string, rels []string) error {
	tw := tar.NewWriter(w)
	for _, rel := range rels {
		if !fs.ValidPath(rel) {
			continue
		}
		full := filepath.Join(dir, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(full)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, io.LimitReader(f, hdr.Size))
		_ = f.Close() // read only
		if err != nil {
			return err
		}
	}
	return tw.Close()
}

// abandon closes a sandbox that could not be made ready, on a context that is
// not the caller's, and returns the failure that made it so.
func (s *Session) abandon(ctx context.Context, failure error) error {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	err := s.sb.Close(closeCtx)
	if s.closer != nil {
		err = errors.Join(err, s.closer.Close())
	}
	if err != nil && s.req.Warn != nil {
		// The failure of the setup is what the caller is told; the close is a warning.
		s.req.Warn("box_close_failed", fmt.Sprintf("the %s box could not be closed: %v", s.provider, err))
	}
	s.record("box_close", map[string]any{"provider": s.provider})
	return failure
}

// record notes an entry in the run's ledger. A ledger that cannot be written
// is a warning, once, and does not stop the run.
func (s *Session) record(kind string, data any) {
	if s.req.Record == nil {
		return
	}
	if err := s.req.Record(kind, data); err != nil && s.req.Warn != nil {
		s.ledgerWarned.Do(func() {
			s.req.Warn("ledger_unwritable", fmt.Sprintf("the ledger entry %s could not be written: %v", kind, err))
		})
	}
}

// agentDir is where af keeps its files in the tree.
const agentDir = agentdir.Name + "/"

// WriteFile writes a run file into the box, through the box's write tool. It
// writes only paths that pass fs.ValidPath and lie under .agent-fox/, and only
// text: the session writes run files (the brief, the run's number, the hook's
// configuration) and nothing else, so another path, or bytes the tool would
// change, is a bug of the caller and an internal error, not a usage failure.
func (s *Session) WriteFile(ctx context.Context, rel string, data []byte) error {
	if !fs.ValidPath(rel) || !strings.HasPrefix(rel, agentDir) {
		return fmt.Errorf("box: WriteFile writes only under %s, not %q", agentDir, rel)
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("box: WriteFile writes text, and the content of %s is not valid UTF-8", rel)
	}
	r, err := s.call(ctx, toolWriteFile, map[string]any{"path": rel, "content": string(data)})
	if err != nil {
		return err
	}
	if !r.OK {
		return s.goneIf(resultGone(r), &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: fmt.Sprintf("the %s box could not write %s: %s %s", s.provider, rel, r.Error, r.Detail)})
	}
	return nil
}

// Export exports the box's tree into a fresh directory under the temporary
// directory of the OS, af-export-<run_id>-*, never inside the checkout, and
// records box_export. The directory is removed by Close. The tree comes out
// without .git, and without what the hands' rules ignore, the run files of
// .agent-fox/ among it. An export that carries a .git is removed and fails: it
// breaks what the box is for.
func (s *Session) Export(ctx context.Context) (string, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return "", errors.New("box: Export on a closed session")
	}
	pattern := "af-export-" + strings.NewReplacer("/", "_", string(filepath.Separator), "_").Replace(s.req.RunID) + "-*"
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		return "", &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot make a directory for the export of the box: %v", err)}
	}
	if err := s.export(ctx, ".", dir); err != nil {
		_ = os.RemoveAll(dir)
		return "", s.goneIf(isGone(err), &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: fmt.Sprintf("the %s box could not be exported: %v", s.provider, err)})
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
		_ = os.RemoveAll(dir)
		return "", &toolio.Failure{Category: toolio.CategorySandbox, Message: fmt.Sprintf("the %s box's export carried .git, which no export may: the run stops", s.provider)}
	}
	s.mu.Lock()
	if s.closed {
		// Close ran while the tree came out, and has no list with this directory.
		s.mu.Unlock()
		_ = os.RemoveAll(dir)
		return "", errors.New("box: the session was closed during Export")
	}
	s.exports = append(s.exports, dir)
	s.mu.Unlock()
	s.record("box_export", map[string]any{"path": dir})
	return dir, nil
}

// Close closes the sandbox, removes every export directory the session made
// and records box_close. It is idempotent: later calls do nothing and return
// nil. The close is bounded to 30 seconds, on a context of its own.
func (s *Session) Close() error { return s.closeWith(context.Background()) }

// closeWith is Close on a context that carries the values of ctx but not its
// cancellation.
func (s *Session) closeWith(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	exports := s.exports
	s.exports = nil
	s.mu.Unlock()

	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	err := s.sb.Close(closeCtx)
	if s.closer != nil {
		err = errors.Join(err, s.closer.Close())
	}
	for _, dir := range exports {
		if rerr := os.RemoveAll(dir); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}
	s.record("box_close", map[string]any{"provider": s.provider})
	return err
}

// export exports what is at name in the box into the directory dst, one
// transfer at a time.
func (s *Session) export(ctx context.Context, name, dst string) error {
	s.stream.Lock()
	defer s.stream.Unlock()
	return s.sb.Export(ctx, name, sandbox.LocalDir{Path: dst})
}

// call runs one of the box's tools with the arguments and returns its result.
func (s *Session) call(ctx context.Context, name string, args map[string]any) (core.ToolResult, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return core.ToolResult{}, err
	}
	for _, t := range s.tools {
		if t.Name == name {
			return s.watch(t).Execute(ctx, raw), nil
		}
	}
	return core.ToolResult{}, fmt.Errorf("the box has no %s tool", name)
}
