package box

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

// closeTimeout bounds the close of a sandbox that was set up and could not be
// used, which runs on a context detached from the caller's.
const closeTimeout = 30 * time.Second

// Session is one held box: a sandbox with one workspace, and the host
// directory it was synced from. The host directory is never written.
type Session struct {
	req      Request
	sb       sandbox.Sandbox
	tools    []core.Tool
	provider string
	hostDir  string

	ledgerWarned sync.Once
}

// Provider is the name of the provider.
func (s *Session) Provider() string { return s.provider }

// Spec is the spec the box was created with, as the provider took it.
func (s *Session) Spec() sandbox.Spec { return s.sb.Spec() }

// Tools is the box's tool set, for brains: one proxy for each of the hands'
// built-in tools, whose every call runs inside the box.
func (s *Session) Tools() []core.Tool { return s.tools }

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
// is removed and sent again. A file of the tree that the host changed within the
// second of the last sync, without changing its length, is still not seen.
func (s *Session) Sync(ctx context.Context) error {
	if err := s.refreshGit(ctx); err != nil {
		return err
	}
	return s.sync(ctx)
}

// refreshGit removes git's own state from the box, for the sync to send again.
func (s *Session) refreshGit(ctx context.Context) error {
	const command = "[ -d .git ] && find .git -mindepth 1 -maxdepth 1 ! -name objects -exec rm -rf {} + ; true"
	r, err := s.call(ctx, toolExecute, map[string]any{"command": command})
	if err == nil && !r.OK {
		err = fmt.Errorf("%s: %s", r.Error, r.Detail)
	}
	if err != nil {
		return &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: fmt.Sprintf("the %s box could not be prepared for a sync: %v", s.provider, err)}
	}
	return nil
}

// sync syncs the host directory in with its .git and records box_sync with the
// host's short HEAD. A repository with no commit has none to record.
func (s *Session) sync(ctx context.Context) error {
	if _, err := s.sb.Sync(ctx, syncSource(s.hostDir)); err != nil {
		return &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: fmt.Sprintf("the %s box could not be synced from %s: %v", s.provider, s.hostDir, err)}
	}
	head, _ := gitx.New(s.hostDir, nil).Head(ctx)
	s.record("box_sync", map[string]any{"head": head})
	return nil
}

// abandon closes a sandbox that could not be made ready, on a context that is
// not the caller's, and returns the failure that made it so.
func (s *Session) abandon(ctx context.Context, failure error) error {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	_ = s.sb.Close(closeCtx) // the failure of the setup is what the caller is told
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
