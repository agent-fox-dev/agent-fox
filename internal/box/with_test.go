package box

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

var errFn = errors.New("fn failed")

func TestWith_ClosesTheBoxHoweverFnEndsAndPassesTheOutcomeOn(t *testing.T) {
	dir := hostRepo(t, nil)
	for _, tc := range []struct {
		name string
		fn   func(cancel func()) error
		want func(err error, panicked any) bool
	}{
		{"a normal return", func(func()) error { return nil }, func(err error, p any) bool { return err == nil && p == nil }},
		{"an error", func(func()) error { return errFn }, func(err error, p any) bool { return errors.Is(err, errFn) && p == nil }},
		{"a panic", func(func()) error { panic("a value") }, func(_ error, p any) bool { return p == "a value" }},
		{"a cancelled context", func(cancel func()) error { cancel(); return context.Canceled }, func(err error, p any) bool { return errors.Is(err, context.Canceled) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{}
			l := &ledger{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			start := time.Now()

			err, panicked := func() (err error, panicked any) {
				defer func() { panicked = recover() }()
				return With(ctx, request("fake", dir, registry(f.entry()), l), func(*Session) error { return tc.fn(cancel) }), nil
			}()

			if !tc.want(err, panicked) {
				t.Errorf("With = %v, panicked %v", err, panicked)
			}
			if f.creates.Load() != 1 || f.closes.Load() != 1 {
				t.Fatalf("%d boxes made, %d closed, want 1 and 1", f.creates.Load(), f.closes.Load())
			}
			if call := f.closeCalls[0]; call.err != nil || !call.hasDeadline || call.deadline.After(start.Add(31*time.Second)) {
				t.Errorf("the close ran on a context with error %v and deadline %v (%v), want one not cancelled, bounded to 30 s", call.err, call.deadline, call.hasDeadline)
			}
			if got := l.entries(); !slices.Equal(got, []string{"box_open", "box_sync", "box_close"}) {
				t.Errorf("ledger = %v, want box_open, box_sync, box_close", got)
			}
		})
	}
}

func TestWith_AnOpenThatFailsIsReturnedAndFnIsNotCalled(t *testing.T) {
	called := false

	err := With(context.Background(), request("docker", t.TempDir(), NewRegistry(), &ledger{}), func(*Session) error { called = true; return nil })

	if f := asFailure(t, err); called || f.Category != toolio.CategoryUsage {
		t.Errorf("With = %+v, fn called %v; want the usage failure and no call", f, called)
	}
}

func TestWith_ACloseThatFailsIsAWarningAndChangesNothing(t *testing.T) {
	dir := hostRepo(t, nil)
	r := registry((&fake{closeErr: errors.New("the box would not go")}).entry())
	l := &ledger{}

	errNil := With(context.Background(), request("fake", dir, r, l), func(*Session) error { return nil })
	errFnErr := With(context.Background(), request("fake", dir, r, l), func(*Session) error { return errFn })

	if errNil != nil || !errors.Is(errFnErr, errFn) {
		t.Errorf("With = %v and %v, want nil and fn's error", errNil, errFnErr)
	}
	if warned := l.warned("box_close_failed"); len(warned) != 2 || !strings.Contains(warned[1].Message, "the fake box could not be closed: the box would not go") {
		t.Errorf("warnings = %+v, want a box_close_failed for each", l.warnings)
	}
}
