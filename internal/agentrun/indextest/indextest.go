// Package indextest holds the recording tools.Index double and the shared
// lifecycle tests of the four cmd/ entry points (spec 16_indexed_code_search,
// 16-REQ-3): each entry point builds one code-search index per run, hands the
// same instance to its Runner and its tool's Options, and closes it once on
// every exit path.
//
// It is imported only from tests.
package indextest

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/tools"
)

// seq orders events across every Index and Factory in the process, so a test
// can say that an Invalidate came before a phase started.
var seq atomic.Int64

// Next returns the next sequence number. A test double for a phase calls it
// to stamp the moment the phase starts.
func Next() int64 { return seq.Add(1) }

// Event is one recorded call on an Index.
type Event struct {
	// Kind is "invalidate" or "close".
	Kind string
	// Rel is the path argument of an invalidate.
	Rel string
	// Seq is the call's place in the process-wide order; see Next.
	Seq int64
}

// Index is a tools.Index that records Invalidate and Close calls. Its Tools
// returns a code_search tool with an Execute func, as the real index does.
type Index struct {
	mu     sync.Mutex
	events []Event
}

var _ tools.Index = (*Index)(nil)

// Symbols never answers: the caller falls back to its own table.
func (*Index) Symbols(context.Context, tools.SymbolQuery) (tools.SymbolAnswer, bool, error) {
	return tools.SymbolAnswer{}, false, nil
}

// Tools returns a code_search tool.
func (*Index) Tools() []core.Tool {
	return []core.Tool{{
		Name: "code_search", Description: "ranked search",
		Execute: func(context.Context, json.RawMessage) core.ToolResult { return core.OKResult(map[string]any{}) },
	}}
}

// Invalidate records the call.
func (i *Index) Invalidate(rel string) { i.record("invalidate", rel) }

// Close records the call. Like the real index's, it is safe to call twice;
// CloseCalls is how a test sees that it was.
func (i *Index) Close() error { i.record("close", ""); return nil }

func (i *Index) record(kind, rel string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.events = append(i.events, Event{Kind: kind, Rel: rel, Seq: Next()})
}

// Events returns a copy of the recorded calls, in order.
func (i *Index) Events() []Event {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]Event(nil), i.events...)
}

func (i *Index) count(kind string) int {
	n := 0
	for _, e := range i.Events() {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// CloseCalls is how many times Close was called.
func (i *Index) CloseCalls() int { return i.count("close") }

// InvalidateCalls is how many times Invalidate was called.
func (i *Index) InvalidateCalls() int { return i.count("invalidate") }

// Factory stands in for codesearch.New: it counts the builds, remembers the
// workspace each was for, and either returns Index or Err.
type Factory struct {
	// Err, when set, is what every build returns, with a nil index.
	Err error
	// Index is what a successful build returns; New creates one when nil.
	Index *Index

	mu         sync.Mutex
	workspaces []*tools.Workspace
}

// New is the function an entry point's newIndex variable is set to.
func (f *Factory) New(ws *tools.Workspace) (tools.Index, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workspaces = append(f.workspaces, ws)
	if f.Err != nil {
		return nil, f.Err
	}
	if f.Index == nil {
		f.Index = &Index{}
	}
	return f.Index, nil
}

// NewCalls is how many times New was called.
func (f *Factory) NewCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.workspaces)
}

// Workspaces returns the workspace of each build, in order.
func (f *Factory) Workspaces() []*tools.Workspace {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*tools.Workspace(nil), f.workspaces...)
}
