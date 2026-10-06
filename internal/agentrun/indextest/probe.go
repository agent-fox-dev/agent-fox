package indextest

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"

	"github.com/agentfox/agentkit-go/core"
)

// Snap is the tree at the moment of an Invalidate: HEAD, the checked-out
// branch and `git status`. "After the commit" is then a fact about the
// snapshot — HEAD is the commit and the status is empty — rather than about
// the order of two log lines.
type Snap struct {
	Seq    int64
	Rel    string
	Head   string
	Branch string
	Status string
}

// Probe is an Index for the smoke tests that run a whole entry point: it
// stamps every execution of its code_search tool, so a test can say which
// phase a search ran in, and snapshots the tree on every Invalidate, so a test
// can say what state the tree was in when the index was told to forget.
//
// Set Root (the run's workspace root) before the run starts.
type Probe struct {
	Index
	// Root is the repository the snapshots are taken of.
	Root string

	mu       sync.Mutex
	searches []int64
	snaps    []Snap
}

// Tools returns a code_search tool that records each call.
func (p *Probe) Tools() []core.Tool {
	return []core.Tool{{
		Name: "code_search", Description: "ranked search",
		Execute: func(context.Context, json.RawMessage) core.ToolResult {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.searches = append(p.searches, Next())
			return core.OKResult(map[string]any{"hits": []any{}})
		},
	}}
}

// Invalidate records the call and the tree it was made on.
func (p *Probe) Invalidate(rel string) {
	p.Index.Invalidate(rel)
	snap := Snap{Seq: Next(), Rel: rel}
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = p.Root
		out, _ := cmd.Output()
		return strings.TrimSpace(string(out))
	}
	snap.Head = git("rev-parse", "HEAD")
	snap.Branch = git("rev-parse", "--abbrev-ref", "HEAD")
	snap.Status = git("status", "--porcelain", "-uall")
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snaps = append(p.snaps, snap)
}

// Searches returns the sequence number of each code_search execution, in order.
func (p *Probe) Searches() []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int64(nil), p.searches...)
}

// Snaps returns a copy of the snapshots, in order.
func (p *Probe) Snaps() []Snap {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Snap(nil), p.snaps...)
}

// Between returns the snapshots taken after lo and before hi.
func (p *Probe) Between(lo, hi int64) []Snap {
	var out []Snap
	for _, s := range p.Snaps() {
		if s.Seq > lo && s.Seq < hi {
			out = append(out, s)
		}
	}
	return out
}
