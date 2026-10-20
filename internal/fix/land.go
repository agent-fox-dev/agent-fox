package fix

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The side effects and the artifacts of a landing or a parking.
const (
	EffectBranch            = "branch"
	EffectCommit            = "commit"
	EffectPush              = "push"
	EffectCreateIssue       = "create_issue"
	EffectCreatePullRequest = "create_pull_request"
)

// The heading of the original report in the issue that is made for one, and the line that
// ends the body.
const originalHeading = "## Original report"

// Reporter is where a landing says what it did: its warnings, what it produced, what it
// wrote beyond the working tree, and what to do next. A *toolio.Run is one.
type Reporter interface {
	Warn(code, message string)
	Artifact(a toolio.Artifact)
	SideEffect(s toolio.SideEffect)
	Next(n toolio.Next)
}

// IssueRef is an issue that was created.
type IssueRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// PullRequestRef is a pull request that was opened.
type PullRequestRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
}

// runMarker is the comment that tells which run a pull request is from.
func runMarker(runID string) string { return "<!-- agent-fox:run " + runID + " -->" }

// BranchName is fix/<N>-<slug> for an issue, and fix/<slug> without one, where the slug is
// that of the title.
func BranchName(number int, title string) string {
	if number > 0 {
		return fmt.Sprintf("fix/%d-%s", number, gitx.Slug(title))
	}
	return "fix/" + gitx.Slug(title)
}

// MakeBranch names the branch for the issue and the title, makes the name unique among the
// local and the remote branches, creates it at the commit the run began at with the
// working tree carried along, and reports it.
func MakeBranch(ctx context.Context, g *gitx.Git, rep Reporter, number int, title, baseHead string) (string, error) {
	name := gitx.UniqueBranchName(ctx, g, BranchName(number, title))
	if err := g.CreateBranchFrom(ctx, name, baseHead); err != nil {
		rep.SideEffect(toolio.SideEffect{Kind: EffectBranch, Target: name, State: toolio.StateFailed, Detail: err.Error()})
		return "", &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot create the branch %s at %s: %v", name, baseHead, err)}
	}
	rep.SideEffect(toolio.SideEffect{Kind: EffectBranch, Target: name, State: toolio.StateDone})
	rep.Artifact(toolio.Artifact{Kind: "branch", Name: name})
	return name, nil
}

// NeedsIssue says whether a report of this kind gets an issue made for it: a text, a file
// or standard input, when the pull request is asked for.
func NeedsIssue(kind, mode string) bool {
	return mode == LandPR && (kind == "text" || kind == "file" || kind == "stdin")
}

// IssueParams is what the issue for a report is made of.
type IssueParams struct {
	Forge  issuex.Client
	Target issuex.Repo
	// Title is the subject of the fix, Original the report as it was given, and RunID the
	// run that names the line at the end of the body.
	Title, Original, RunID string
	Report                 Reporter
}

// CreateIssue creates the issue for a text or a file report: the subject is its title and
// the original report, under a heading, is its body, with a line that names the run. A
// report that does not fit what the forge takes is cut, behind the marker the shell cuts
// an input with.
func CreateIssue(ctx context.Context, p IssueParams) (IssueRef, error) {
	body := issueBody(p.Original, p.RunID, issuex.MaxBodyLength(p.Forge))
	issue, err := p.Forge.CreateIssue(ctx, p.Target, issuex.CreateIssueRequest{Title: p.Title, Body: body})
	target := p.Target.String()
	if err != nil {
		p.Report.SideEffect(toolio.SideEffect{Kind: EffectCreateIssue, Target: target, State: toolio.StateFailed, Detail: err.Error()})
		return IssueRef{}, forgeFailure(ctx, "create the issue in "+target, err)
	}
	ref := IssueRef{Number: issue.Number, URL: firstNonEmpty(issue.HTMLURL, issue.URL)}
	p.Report.SideEffect(toolio.SideEffect{Kind: EffectCreateIssue, Target: target, State: toolio.StateDone})
	p.Report.Artifact(toolio.Artifact{Kind: "issue", URL: ref.URL, Number: ref.Number})
	return ref, nil
}

// issueBody is the body of the issue for a report, within limit bytes.
func issueBody(original, runID string, limit int) string {
	head := originalHeading + "\n\n"
	tail := fmt.Sprintf("\n\nCreated by af fix, run %s.\n", runID)
	if room := limit - len(head) - len(tail); len(original) > room {
		original = cutAtRune(original, max(0, room-len(toolio.TruncationMarker))) + toolio.TruncationMarker
	}
	return head + original + tail
}

// cutAtRune is s cut to at most n bytes at the end of a character.
func cutAtRune(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// forgeFailure is the failure for an error of a write to the forge, which is what names.
func forgeFailure(ctx context.Context, what string, err error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, issuex.ErrNoToken) || issuex.IsNoToken(err) || isUnauthorized(err):
		return &toolio.Failure{Category: toolio.CategoryAuth, Message: fmt.Sprintf("cannot %s: %v", what, err)}
	}
	return &toolio.Failure{Category: toolio.CategoryForge, Message: fmt.Sprintf("cannot %s: %v", what, err), Retryable: retryable(err)}
}

// oneLine is a subject as a single line.
func singleLine(s string) string { return strings.Join(strings.Fields(s), " ") }
