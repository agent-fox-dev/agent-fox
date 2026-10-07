package agentrun

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/tools"
)

// goTypecheckTimeout bounds the probe that reads AgentKit's References
// summary. It is a variable so a test can shorten it.
var goTypecheckTimeout = 10 * time.Second

// DetectGoTypecheck reports how many Go packages AgentKit checked and how many
// errors it collected under its bounds. The detail is a fixed, unpluralised
// format: '<N> packages checked, <M> errors', with ', partial' appended
// exactly when a bound was hit. An error means the detection itself failed:
// no workspace, find_references not offered, the seam failing or exposing no
// summary, or the probe timeout elapsing. It mirrors DetectSymbolBackend.
//
// It is a variable so a test can simulate a failing detection.
var DetectGoTypecheck = detectGoTypecheck

// goTypecheckProbe is the function that actually calls the AgentKit seam to
// get the packages-checked count, errors-collected count and partial flag.
// It is a variable so the real implementation can be swapped in when AgentKit
// exposes Workspace.References, and tests can inject behaviour.
//
// The default returns an error because the replace target does not yet expose
// the References seam (design decision 8). When AgentKit's spec is merged and
// the replace target carries it, this default is replaced with a call to
// ws.References().
var goTypecheckProbe = defaultGoTypecheckProbe

func defaultGoTypecheckProbe(ctx context.Context, ws *tools.Workspace) (packages, errs int, partial bool, err error) {
	return 0, 0, false, errors.New("the seam exposes no summary: AgentKit's Workspace.References is not yet available")
}

func detectGoTypecheck(ws *tools.Workspace) (string, error) {
	if ws == nil {
		return "", errors.New("no workspace configured")
	}
	built, err := tools.All(tools.Options{Workspace: ws})
	if err != nil {
		return "", fmt.Errorf("building the file tools: %w", err)
	}
	if !slices.ContainsFunc(built, func(t core.Tool) bool { return t.Name == "find_references" }) {
		return "", errors.New("the file tools do not include find_references")
	}

	ctx, cancel := context.WithTimeout(context.Background(), goTypecheckTimeout)
	defer cancel()

	type result struct {
		packages int
		errs     int
		partial  bool
		err      error
	}
	ch := make(chan result, 1)
	go func() {
		p, e, pt, err := goTypecheckProbe(ctx, ws)
		ch <- result{p, e, pt, err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			return "", fmt.Errorf("go typecheck probe: %w", r.err)
		}
		return goTypecheckDetail(r.packages, r.errs, r.partial), nil
	case <-ctx.Done():
		return "", fmt.Errorf("go typecheck probe: %w", context.DeadlineExceeded)
	}
}

// goTypecheckDetail formats the detail string from the three numbers.
func goTypecheckDetail(packages, errs int, partial bool) string {
	s := fmt.Sprintf("%d packages checked, %d errors", packages, errs)
	if partial {
		s += ", partial"
	}
	return s
}
