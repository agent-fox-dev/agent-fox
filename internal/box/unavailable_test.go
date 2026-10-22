package box

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/gateway"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// lossSandbox is a sandbox whose sync and export fail as the test says.
type lossSandbox struct {
	sandbox.Sandbox
	syncErr, exportErr error
}

func (s *lossSandbox) Sync(ctx context.Context, src sandbox.Source) (sandbox.SyncResult, error) {
	if s.syncErr != nil {
		return sandbox.SyncResult{}, s.syncErr
	}
	return s.Sandbox.Sync(ctx, src)
}

func (s *lossSandbox) Export(ctx context.Context, path string, dst sandbox.Destination) error {
	if s.exportErr != nil {
		return s.exportErr
	}
	return s.Sandbox.Export(ctx, path, dst)
}

// stubTool replaces the tool of the session with one that answers with the result.
func stubTool(s *Session, name string, result core.ToolResult) {
	for i, tool := range s.tools {
		if tool.Name == name {
			tool.Execute = func(context.Context, json.RawMessage) core.ToolResult { return result }
			s.tools[i] = tool
		}
	}
}

// TestAGoneOrUnreachableSandboxIsRecognisableAndItsFailureIsUnchanged_TS_14_43 verifies TS-14-43: the
// failure of a box operation that failed because the sandbox is gone or cannot be reached matches
// ErrUnavailable with errors.Is, in a command, a write, a sync, an export and a read of the tree, while
// its category, its retryable flag and its message are what they were; a failure for any other reason
// does not match.
// Verifies: 14-REQ-7.1
func TestAGoneOrUnreachableSandboxIsRecognisableAndItsFailureIsUnchanged_TS_14_43(t *testing.T) {
	gone := core.ErrResult("sandbox_unavailable", "the box is gone")
	other := core.ErrResult("write_failed", "disk full")
	transport := fmt.Errorf("%w: gateway http://gw.invalid: sync git: connection reset", sandbox.ErrUnavailable)
	refused := fmt.Errorf("%w: gateway http://gw.invalid refused the token (sync git): lease expired", gateway.ErrUnauthenticated)

	cases := []struct {
		name string
		lost bool
		// do breaks the session and makes the operation; the failure it returns is checked against
		// the message, category and retryable flag that the operation had before there was a sentinel.
		do        func(s *Session, sb *lossSandbox) error
		message   string
		retryable bool
	}{
		{"a command, the tool says the sandbox is gone", true, func(s *Session, _ *lossSandbox) error {
			stubTool(s, "execute", gone)
			_, _, err := s.Exec(context.Background(), "true", 0)
			return err
		}, "the local box could not run the command: sandbox_unavailable the box is gone", false},
		{"a write, the tool says the sandbox is gone", true, func(s *Session, _ *lossSandbox) error {
			stubTool(s, "write_file", gone)
			return s.WriteFile(context.Background(), ".agent-fox/x", []byte("x"))
		}, "the local box could not write .agent-fox/x: sandbox_unavailable the box is gone", true},
		{"the preparation of a sync, the tool says the sandbox is gone", true, func(s *Session, _ *lossSandbox) error {
			stubTool(s, "execute", gone)
			return s.Sync(context.Background())
		}, "the local box could not be prepared for a sync: sandbox_unavailable: the box is gone", true},
		{"a sync, the transport failed", true, func(s *Session, sb *lossSandbox) error {
			sb.syncErr = transport
			return s.Sync(context.Background())
		}, fmt.Sprintf("the local box could not be synced from %%s: %v", transport), true},
		{"a sync, the gateway refused the token of an expired lease", true, func(s *Session, sb *lossSandbox) error {
			sb.syncErr = refused
			return s.Sync(context.Background())
		}, fmt.Sprintf("the local box could not be synced from %%s: %v", refused), true},
		{"an export, the transport failed", true, func(s *Session, sb *lossSandbox) error {
			sb.exportErr = transport
			_, err := s.Export(context.Background())
			return err
		}, fmt.Sprintf("the local box could not be exported: %v", transport), true},
		{"a read of the tree, the tool says the sandbox is gone", true, func(s *Session, _ *lossSandbox) error {
			stubTool(s, "list_files", gone)
			_, err := fs.ReadDir(s.FS(context.Background()), ".agent-fox")
			return err
		}, "", false},

		{"a command, another reason", false, func(s *Session, _ *lossSandbox) error {
			stubTool(s, "execute", other)
			_, _, err := s.Exec(context.Background(), "true", 0)
			return err
		}, "the local box could not run the command: write_failed disk full", false},
		{"a write, another reason", false, func(s *Session, _ *lossSandbox) error {
			stubTool(s, "write_file", other)
			return s.WriteFile(context.Background(), ".agent-fox/x", []byte("x"))
		}, "the local box could not write .agent-fox/x: write_failed disk full", true},
		{"a sync, another reason", false, func(s *Session, sb *lossSandbox) error {
			sb.syncErr = errors.New("transfer broke")
			return s.Sync(context.Background())
		}, "the local box could not be synced from %s: transfer broke", true},
		{"an export, an unsafe archive", false, func(s *Session, sb *lossSandbox) error {
			sb.exportErr = sandbox.ErrUnsafeArchive
			_, err := s.Export(context.Background())
			return err
		}, fmt.Sprintf("the local box could not be exported: %v", sandbox.ErrUnsafeArchive), true},
		{"a read of the tree, another reason", false, func(s *Session, _ *lossSandbox) error {
			stubTool(s, "list_files", other)
			_, err := fs.ReadDir(s.FS(context.Background()), ".agent-fox")
			return err
		}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t, 1, "main")
			s := open(t, Default, "local", dir, &ledger{})
			sb := &lossSandbox{Sandbox: s.sb}
			s.sb = sb

			err := tc.do(s, sb)

			if err == nil {
				t.Fatal("the operation succeeded")
			}
			if got := errors.Is(err, ErrUnavailable); got != tc.lost {
				t.Errorf("errors.Is(err, ErrUnavailable) = %v for %v, want %v", got, err, tc.lost)
			}
			if tc.message == "" { // a read of the tree is no failure of the shell
				return
			}
			f := asFailure(t, err)
			want := tc.message
			if strings.Contains(want, "%s") {
				want = fmt.Sprintf(want, s.hostDir)
			}
			if f.Category != toolio.CategorySandbox || f.Retryable != tc.retryable || f.Error() != want || err.Error() != want {
				t.Errorf("failure = %q category %q retryable %v (error text %q), want %q, category sandbox, retryable %v", f.Message, f.Category, f.Retryable, err.Error(), want, tc.retryable)
			}
		})
	}
}
