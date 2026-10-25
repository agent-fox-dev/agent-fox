package box

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/gateway"
)

func TestErrUnavailable_MarksAFailureOfASandboxThatIsGoneAndLostNotesIt(t *testing.T) {
	gone := answer(core.ErrResult("sandbox_unavailable", "the box is gone"))
	other := answer(core.ErrResult("write_failed", "disk full"))
	transport := fmt.Errorf("%w: gateway http://gw.invalid: connection reset", sandbox.ErrUnavailable)
	refused := fmt.Errorf("%w: gateway http://gw.invalid refused the token: lease expired", gateway.ErrUnauthenticated)
	exec := func(s *Session) error { _, _, err := s.Exec(context.Background(), "true", 0); return err }
	write := func(s *Session) error { return s.WriteFile(context.Background(), ".agent-fox/x", []byte("x")) }
	sync := func(s *Session) error { return s.Sync(context.Background()) }
	export := func(s *Session) error { _, err := s.Export(context.Background()); return err }
	list := func(s *Session) error { _, err := fs.ReadDir(s.FS(context.Background()), "."); return err }
	read := func(s *Session) error { _, err := fs.ReadFile(s.FS(context.Background()), "file0.txt"); return err }

	// A case in place stubs the tool of a session in place; one with a fault
	// puts it in the box of the fake provider.
	for _, tc := range []struct {
		name      string
		tool      string
		answer    func(map[string]any) core.ToolResult
		fault     func(*fake)
		do        func(*Session) error
		lost      bool
		message   string // of the failure; "" for an error that is no failure
		retryable bool
	}{
		{"a command told the box is gone", "execute", gone, nil, exec, true, "the here box could not run the command: sandbox_unavailable the box is gone", false},
		{"a write told the box is gone", "write_file", gone, nil, write, true, "the here box could not write .agent-fox/x: sandbox_unavailable the box is gone", true},
		{"a listing told the box is gone", "list_files", gone, nil, list, true, "", false},
		{"the preparation of a sync told the box is gone", "", nil, func(*fake) {}, nil, true, "the fake box could not be prepared for a sync: sandbox_unavailable: the box is gone", true},
		{"a sync whose transport failed", "", nil, func(f *fake) { f.syncErr = transport }, sync, true, "the fake box could not be synced from %s: " + transport.Error(), true},
		{"a sync of a lease that ended", "", nil, func(f *fake) { f.syncErr = refused }, sync, true, "the fake box could not be synced from %s: " + refused.Error(), true},
		{"an export whose transport failed", "", nil, func(f *fake) { f.exportErr = transport }, export, true, "the fake box could not be exported: " + transport.Error(), true},
		{"a read whose transport failed", "", nil, func(f *fake) { f.exportErr = transport }, read, true, "", false},

		{"a command that failed otherwise", "execute", other, nil, exec, false, "the here box could not run the command: write_failed disk full", false},
		{"a write that failed otherwise", "write_file", other, nil, write, false, "the here box could not write .agent-fox/x: write_failed disk full", true},
		{"a listing that failed otherwise", "list_files", other, nil, list, false, "", false},
		{"a sync that failed otherwise", "", nil, func(f *fake) { f.syncErr = errors.New("transfer broke") }, sync, false, "the fake box could not be synced from %s: transfer broke", true},
		{"an export of an unsafe archive", "", nil, func(f *fake) { f.exportErr = sandbox.ErrUnsafeArchive }, export, false, "the fake box could not be exported: " + sandbox.ErrUnsafeArchive.Error(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s *Session
			if tc.fault == nil {
				s = openHere(t, plainDir(t, nil), &ledger{})
				stubTool(s, tc.tool, tc.answer)
			} else {
				f := &fake{}
				s = f.open(t, hostRepo(t, nil), &ledger{})
				tc.fault(f)
				if tc.do == nil { // the sync's preparation runs a command first
					stubTool(s, "execute", gone)
					tc.do = sync
				}
			}

			err := tc.do(s)

			if got := errors.Is(err, ErrUnavailable); err == nil || got != tc.lost {
				t.Errorf("errors.Is(%v, ErrUnavailable) = %v, want %v", err, got, tc.lost)
			}
			if got := errors.Is(s.Lost(), ErrUnavailable); got != tc.lost {
				t.Errorf("Lost() = %v, want lost %v", s.Lost(), tc.lost)
			}
			if tc.message == "" {
				return
			}
			want := strings.ReplaceAll(tc.message, "%s", s.HostDir())
			if f := asFailure(t, err); f.Category != toolio.CategorySandbox || f.Retryable != tc.retryable || err.Error() != want {
				t.Errorf("failure = %q, %s, retryable %v; want %q, sandbox, retryable %v", err, f.Category, f.Retryable, want, tc.retryable)
			}
		})
	}
}

func TestLost_NotesABrainsCallThatWasToldTheBoxIsGone(t *testing.T) {
	s := openHere(t, plainDir(t, nil), &ledger{})
	stubTool(s, "execute", answer(core.ErrResult("write_failed", "disk full")))
	callTool(t, s, "execute", map[string]any{"command": "true"})
	if s.Lost() != nil {
		t.Errorf("a call that failed otherwise lost the box: %v", s.Lost())
	}

	stubTool(s, "execute", answer(core.ErrResult("sandbox_unavailable", "the box is gone")))
	callTool(t, s, "execute", map[string]any{"command": "true"})
	stubTool(s, "execute", answer(core.ErrResult("sandbox_unavailable", "a later word")))
	callTool(t, s, "execute", map[string]any{"command": "true"})

	if err := s.Lost(); !errors.Is(err, ErrUnavailable) || err.Error() != "sandbox_unavailable: the box is gone" {
		t.Errorf("Lost() = %v, want the first loss, with what the box said", err)
	}
}

func TestGone_OfNoErrorIsNone(t *testing.T) {
	if gone(nil) != nil {
		t.Error("gone(nil) is an error")
	}
}
