package agentrun

import (
	"context"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"
)

func indexRunner(t *testing.T, idx tools.Index) *Runner {
	t.Helper()
	cfg := fauxConfig(faux.New(), newWorkspace(t))
	cfg.Index = idx
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The Runner reports the index its Config carries, so a caller can see which
// index its phases read (16-REQ-1.1).
func TestRunnerReportsItsIndex(t *testing.T) {
	idx := &fakeIndex{}
	if got := indexRunner(t, idx).Index(); got != tools.Index(idx) {
		t.Errorf("Index() = %v, want the Config's %v", got, idx)
	}
	if got := indexRunner(t, nil).Index(); got != nil {
		t.Errorf("Index() without one = %v, want nil", got)
	}
}

// valueIndex is an index whose dynamic type == cannot compare.
type valueIndex struct{ tags []string }

func (valueIndex) Symbols(context.Context, tools.SymbolQuery) (tools.SymbolAnswer, bool, error) {
	return tools.SymbolAnswer{}, false, nil
}
func (valueIndex) Tools() []core.Tool { return nil }
func (valueIndex) Invalidate(string)  {}
func (valueIndex) Close() error       { return nil }

// RunIndex holds a pipeline's Options.Index and its Runners' Config.Index to
// being the one index of the run.
func TestRunIndex(t *testing.T) {
	a, b := &fakeIndex{}, &fakeIndex{}
	withA, withB, without := indexRunner(t, a), indexRunner(t, b), indexRunner(t, nil)

	for _, tc := range []struct {
		name    string
		opt     tools.Index
		runners []*Runner
		want    tools.Index
		wantErr bool
	}{
		{"the same index on both", a, []*Runner{withA}, a, false},
		{"the Options carry none: the Runner's is adopted", nil, []*Runner{withA}, a, false},
		{"the Runner has none", a, []*Runner{without}, a, false},
		{"neither has one", nil, []*Runner{without}, nil, false},
		{"no Runner (an injected brain)", a, []*Runner{nil}, a, false},
		{"two runners on the one index", nil, []*Runner{withA, nil, withA}, a, false},
		{"a different index on the Runner", a, []*Runner{withB}, nil, true},
		{"a second runner on another index", nil, []*Runner{withA, withB}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RunIndex(tc.opt, tc.runners...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("RunIndex = %v, want %v", got, tc.want)
			}
		})
	}

	// An index whose type == cannot compare is taken as different, without a
	// panic.
	v := valueIndex{tags: []string{"x"}}
	if _, err := RunIndex(v, indexRunner(t, valueIndex{tags: []string{"x"}})); err == nil {
		t.Error("two uncomparable indexes were taken for the same one")
	}
}
