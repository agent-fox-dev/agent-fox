package specgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// SplitPlanSuffix is the file name suffix of an unfinished split's plan,
// which lives in the spec root beside the packages it describes.
//
// The plan is the one piece of state the tool keeps between runs, and it is
// kept only while the work it describes is unfinished. A run that was cut
// short after writing two of four packages leaves the plan behind; the next
// run with the same input finds it and continues with the third scope, and
// the run that writes the last package removes it. Its presence therefore
// means exactly one thing — `spec` stopped before it was done — which is why
// it is a visible file rather than something hidden in a package.
const SplitPlanSuffix = ".split.json"

// SplitPlan is the record of an input that was more than one spec's worth of
// work: every scope the PRD phase carved it into, in the order they are
// written, and which of them exist.
type SplitPlan struct {
	// Input identifies what the split was planned for, so a run with a
	// different input does not resume someone else's work.
	Input PlanInput `json:"input"`
	// CreatedAt is when the split was planned; UpdatedAt when a scope was
	// last written.
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	// Scopes are the specs, first one first.
	Scopes []PlanScope `json:"scopes"`
}

// PlanInput is the identity of the input a plan belongs to.
type PlanInput struct {
	Kind   string `json:"kind"`
	Origin string `json:"origin"`
	SHA256 string `json:"sha256"`
}

// PlanScope is one spec of the split.
type PlanScope struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
	// SpecID and Dir are set once the package exists. Dir is the package's
	// directory name under the spec root.
	SpecID string `json:"spec_id,omitempty"`
	Dir    string `json:"dir,omitempty"`
	// Invalid records a package that was written and does not validate. It
	// is on disk and is not written again; a person fixes it.
	Invalid bool `json:"invalid,omitempty"`
}

// Written reports whether the scope's package exists.
func (s PlanScope) Written() bool { return s.Dir != "" }

// newSplitPlan starts a plan from what the PRD phase reported.
func newSplitPlan(in toolio.Input, scopes []SplitScope) *SplitPlan {
	now := time.Now().UTC().Format(time.RFC3339)
	plan := &SplitPlan{Input: planInputOf(in), CreatedAt: now, UpdatedAt: now}
	for _, s := range scopes {
		plan.Scopes = append(plan.Scopes, PlanScope{
			Name:  strings.TrimSpace(s.Name),
			Scope: strings.TrimSpace(s.Scope),
		})
	}
	return plan
}

func planInputOf(in toolio.Input) PlanInput {
	sum := sha256.Sum256([]byte(in.Body))
	return PlanInput{Kind: in.Kind.String(), Origin: in.Origin, SHA256: hex.EncodeToString(sum[:])}
}

// Pending is the number of scopes not yet written.
func (p *SplitPlan) Pending() int {
	n := 0
	for _, s := range p.Scopes {
		if !s.Written() {
			n++
		}
	}
	return n
}

// Done reports whether every scope has a package.
func (p *SplitPlan) Done() bool { return p.Pending() == 0 }

// Path is where the plan is kept: named after its first scope, in the spec
// root, where it sorts beside the packages it describes.
func (p *SplitPlan) Path(specsDir string) string {
	return filepath.Join(specsDir, p.Scopes[0].Name+SplitPlanSuffix)
}

// matches reports whether the plan was made for this input.
//
// A file or issue input is matched by where it came from, so an input that
// was edited between runs — the usual reason a run is repeated — still
// resumes rather than starting a second copy of the first package. Text and
// stdin have no origin worth matching ("argument" is every text input's
// origin), so they match by content. The second return value says the
// content differs, which the caller reports because the follow-on PRDs are
// written from the current text.
func (p *SplitPlan) matches(in toolio.Input) (ok, changed bool) {
	cur := planInputOf(in)
	if cur.SHA256 == p.Input.SHA256 {
		return true, false
	}
	switch in.Kind {
	case toolio.KindFile, toolio.KindIssue:
		if cur.Kind == p.Input.Kind && cur.Origin == p.Input.Origin {
			return true, true
		}
	}
	return false, false
}

// save writes the plan atomically enough for its purpose: a partial write
// would be reported as unreadable and ignored, not resumed from.
func (p *SplitPlan) save(specsDir string) error {
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	path := p.Path(specsDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// remove deletes the plan file. A plan that is already gone is not an error.
func (p *SplitPlan) remove(specsDir string) error {
	err := os.Remove(p.Path(specsDir))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// loadSplitPlan reads one plan file.
func loadSplitPlan(path string) (*SplitPlan, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p SplitPlan
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(p.Scopes) < 2 {
		return nil, fmt.Errorf("%s: a split plan needs at least two scopes", path)
	}
	for i, s := range p.Scopes {
		if !specNameRE.MatchString(s.Name) {
			return nil, fmt.Errorf("%s: scope %d has an unusable name %q", path, i+1, s.Name)
		}
	}
	return &p, nil
}

// findSplitPlan looks in the spec root for an unfinished split made for this
// input.
//
// Every plan that is not this input's is reported as a warning rather than
// silently passed over: a stray plan means an earlier run stopped early on
// some other input, and the person running this one is the person who can
// finish or delete it.
func findSplitPlan(specsDir string, in toolio.Input, run *toolio.Run) (*SplitPlan, error) {
	matches, err := filepath.Glob(filepath.Join(specsDir, "*"+SplitPlanSuffix))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	var found *SplitPlan
	for _, path := range matches {
		p, err := loadSplitPlan(path)
		if err != nil {
			run.Warn("an unreadable split plan was ignored: %v", err)
			continue
		}
		ok, changed := p.matches(in)
		if !ok {
			run.Warn("an unfinished split for another input (%s) is at %s; finish it by running "+
				"spec on that input again, or delete the file", p.Input.Origin, path)
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("two unfinished splits match this input: %s and %s; delete the "+
				"one that is wrong", found.Path(specsDir), path)
		}
		if changed {
			run.Warn("the input changed since the split was planned at %s; the remaining scopes "+
				"are written from the current text against the planned split", path)
		}
		found = p
	}
	return found, nil
}

// errPlanExists is returned when a fresh split would overwrite another
// input's unfinished plan of the same name.
var errPlanExists = errors.New("an unfinished split with this name exists for a different input")

// checkPlanSlot refuses to plan a split whose file already belongs to a
// different input. The caller has already looked for a plan that matches, so
// an existing file here is someone else's work in progress.
func checkPlanSlot(p *SplitPlan, specsDir string) error {
	path := p.Path(specsDir)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%w: %s", errPlanExists, path)
	}
	return nil
}

// splitContext tells the PRD phase which scope of a decided split to write.
type splitContext struct {
	Plan  *SplitPlan
	Index int
}

// Scope is the scope being written.
func (c *splitContext) Scope() PlanScope { return c.Plan.Scopes[c.Index] }

// splitBlock renders the scope instructions for a follow-on PRD phase.
//
// The split is decided in Go before this phase starts, so the block states
// it as a fact: which scopes exist, which is being written, and that the
// name is fixed. The model's job narrows to writing one PRD well, against the
// packages that already exist, rather than re-deciding the shape of the work.
func splitBlock(c *splitContext) string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	sc := c.Scope()
	fmt.Fprintf(&b, "\n## This PRD is one scope of a split\n\n")
	fmt.Fprintf(&b, "The input above is more than one spec's worth of work. It has been split "+
		"into %d specs, written in this order:\n\n", len(c.Plan.Scopes))
	b.WriteString("| # | Spec | Scope | State |\n|---|---|---|---|\n")
	for i, s := range c.Plan.Scopes {
		state := "not yet written"
		label := "`" + s.Name + "`"
		switch {
		case i == c.Index:
			state = "**this PRD**"
		case s.Written():
			state = "written"
			label = "`" + s.Dir + "`"
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s |\n", i+1, label, oneLine(s.Scope), state)
	}
	fmt.Fprintf(&b, "\nWrite the PRD for scope %d only: **%s** — %s\n\n", c.Index+1, sc.Name, oneLine(sc.Scope))
	fmt.Fprintf(&b, "- Its `spec_name` is `%s`. Submit exactly that name.\n", sc.Name)
	b.WriteString("- Cover nothing an earlier spec of this split already covers, and nothing a " +
		"later one will. Read the earlier packages under the spec root: their PRDs state what " +
		"they deliver, and this PRD builds on it rather than restating it.\n")
	b.WriteString("- Where this scope consumes what an earlier spec of this split delivers, name " +
		"that spec in `## Dependencies` with the reason.\n")
	b.WriteString("- The split has been decided. Do not report a `recommended_split`; if this scope " +
		"still feels large, keep it to its foundations and say so in `## Non-goals`.\n")
	return b.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
