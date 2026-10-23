package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The kinds of the comments of the mirror.
const (
	commentQuestion   = "question"
	commentReason     = "reason"
	commentFixSummary = "fix_summary"
	commentFailure    = "failure"
)

// loopResult is what the mirror reads of the result of the loop, of a triage and of a fix.
type loopResult struct {
	// Of a fix.
	Outcome     string  `json:"outcome"`
	Land        string  `json:"land"`
	Branch      *string `json:"branch"`
	Verdict     *string `json:"verdict"`
	Closes      bool    `json:"closes"`
	PullRequest *struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
		Draft  bool   `json:"draft"`
	} `json:"pull_request"`
	RevertCheck *struct {
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	} `json:"revert_check"`
	// Of a triage; Issue is the issue, an object, and of a fix the URL of the issue, a string.
	Action    string `json:"action"`
	Diagnosis *struct {
		Confidence string `json:"confidence"`
	} `json:"diagnosis"`
	Issue json.RawMessage `json:"issue"`
}

// readResult reads the result of an envelope, as far as it goes.
func readResult(env toolio.Envelope) loopResult {
	var res loopResult
	if data, err := json.Marshal(env.Result); err == nil {
		_ = json.Unmarshal(data, &res)
	}
	return res
}

func (r loopResult) confidence() string {
	if r.Diagnosis == nil {
		return ""
	}
	return r.Diagnosis.Confidence
}

// land is how the change was landed: the result says, and the setting does when it does not.
func (r loopResult) land(setting string) string {
	if r.Land != "" {
		return r.Land
	}
	return setting
}

// triageIssueNumber is the number of the issue a triage wrote to.
func (r loopResult) triageIssueNumber() int {
	var issue struct {
		Number int `json:"number"`
	}
	if json.Unmarshal(r.Issue, &issue) != nil {
		return 0
	}
	return issue.Number
}

// marker is what a comment of the attempt carries to be known by: the job, the attempt and the
// kind. A comment whose marker is on the issue is not posted again.
func (a *attempt) marker(kind string) string {
	return fmt.Sprintf("<!-- agent-fox job=%s attempt=%d kind=%s -->", a.req.JobID, a.req.Attempt, kind)
}

// comment posts the comment of the kind on the issue, unless the issue has it already, and says whether
// the issue has it now.
func (a *attempt) comment(ctx context.Context, ref issuex.IssueRef, kind string, env toolio.Envelope, res loopResult) bool {
	marker := a.marker(kind)
	if list, err := a.forge.ListComments(ctx, ref); err == nil {
		for _, c := range list.Comments {
			if strings.Contains(c.Body, marker) {
				return true
			}
		}
	}
	body := a.compose(kind, ref, env, res) + "\n\n" + marker + "\n"
	return a.write("add_comment", ref, WarnCommentFailed, "the "+kind+" comment", func() error {
		_, err := a.forge.AddComment(ctx, ref, body)
		return err
	})
}

// compose is the text of a comment, without the marker.
func (a *attempt) compose(kind string, ref issuex.IssueRef, env toolio.Envelope, res loopResult) string {
	job := a.req.JobID
	var b strings.Builder
	switch kind {
	case commentQuestion:
		b.WriteString("**agent-fox needs an answer to go on.**\n\n")
		if nh := env.NeedsHuman; nh != nil {
			b.WriteString(a.scrub(nh.Question) + "\n\n")
			for _, o := range nh.Options {
				fmt.Fprintf(&b, "%s. %s\n", o.ID, a.scrub(o.Label))
			}
			if len(nh.Options) > 0 {
				b.WriteString("\n")
			}
		}
		fmt.Fprintf(&b, "Answer with `af jobs answer %s`, or with `POST /api/v1/jobs/%s/answer`.", job, job)
	case commentReason:
		confidence := res.confidence()
		fmt.Fprintf(&b, "**agent-fox triaged this issue, and is not sure.**\n\nThe confidence of the diagnosis is `%s`. ", confidence)
		if res.Action == "commented" {
			b.WriteString("The diagnosis is in a comment of the triage, because the body would be too long.")
		} else {
			b.WriteString("The diagnosis is in the body of this issue.")
		}
		fmt.Fprintf(&b, "\n\nTo ask again with more to go on, run a new triage of this issue with `context`: `POST /api/v1/workspaces/%s/triage`.", a.req.Workspace)
	case commentFixSummary:
		a.fixSummary(&b, ref, env, res)
	case commentFailure:
		category, message := "", ""
		if env.Error != nil {
			category, message = env.Error.Category, env.Error.Message
		}
		fmt.Fprintf(&b, "**agent-fox could not finish this attempt.**\n\nCategory: `%s`\n\n%s\n\nTo ask again, `POST /api/v1/jobs/%s/retry`.", category, a.scrub(message), job)
	}
	return b.String()
}

// fixSummary writes the comment about a fix: what became of the change, and what was not proven.
func (a *attempt) fixSummary(b *strings.Builder, ref issuex.IssueRef, env toolio.Envelope, res loopResult) {
	verdict, branch := "none", ""
	if res.Verdict != nil {
		verdict = *res.Verdict
	}
	if res.Branch != nil {
		branch = *res.Branch
	}
	parked, kept := res.Outcome == "parked", res.land(a.settings.Land) != "none"
	switch {
	case parked:
		b.WriteString("**agent-fox fix: parked.**\n\nThe change did not land, and is parked on a branch.\n\n")
	case !kept:
		b.WriteString("**agent-fox fix: measured, and nothing kept.**\n\nThe change was measured, and nothing was pushed or kept.\n\n")
	default:
		b.WriteString("**agent-fox fix: landed.**\n\n")
	}
	outcome := "landed"
	switch {
	case parked:
		outcome = "parked"
	case !kept:
		outcome = "measured, nothing kept"
	}
	fmt.Fprintf(b, "- Outcome: %s\n", outcome)
	if branch != "" && (kept || parked) {
		fmt.Fprintf(b, "- Branch: `%s`\n", branch)
	}
	switch {
	case res.PullRequest != nil:
		draft := ""
		if res.PullRequest.Draft {
			draft = " (a draft)"
		}
		fmt.Fprintf(b, "- Pull request: %s%s\n", res.PullRequest.URL, draft)
	case kept && !parked && res.land(a.settings.Land) == "branch":
		b.WriteString("- Pull request: none: no pull request was asked for\n")
	default:
		b.WriteString("- Pull request: none\n")
	}
	fmt.Fprintf(b, "- Verdict: `%s`\n", verdict)
	word := "Refs"
	if res.Closes {
		word = "Closes"
	}
	fmt.Fprintf(b, "- %s %s\n", word, ref.URL())

	var unproven []string
	if !res.Closes {
		reason := "no check showed that a test of its own fails without the change"
		if res.RevertCheck != nil && res.RevertCheck.Reason != "" {
			reason = "the revert check says " + res.RevertCheck.Reason
		}
		unproven = append(unproven, "the change is not proven by a test of its own: "+reason)
	}
	for _, w := range env.Warnings {
		if w.Code == "criteria_unmet" {
			unproven = append(unproven, a.scrub(w.Message))
		}
	}
	if len(unproven) > 0 {
		b.WriteString("\nWhat was not proven:\n")
		for _, u := range unproven {
			fmt.Fprintf(b, "- %s\n", u)
		}
	}
	if parked {
		b.WriteString("\nWhat failed:\n")
		fmt.Fprintf(b, "- the verdict was `%s`\n", verdict)
		for _, w := range env.Warnings {
			if w.Code == "fix_not_landed" || w.Code == "commit_hook_failed" {
				fmt.Fprintf(b, "- %s\n", a.scrub(w.Message))
			}
		}
		fmt.Fprintf(b, "\nTo ask again, `af jobs retry %s`, or `POST /api/v1/jobs/%s/retry`.", a.req.JobID, a.req.JobID)
	}
}

// pathPattern finds the absolute path of a worker in a text: one that begins a word, under a
// directory that a machine keeps its own things in. A route of the API, which begins with /api/,
// and a URL are not found.
var pathPattern = regexp.MustCompile("(^|[\\s'\"(`])/(tmp|var|private|Users|home|root|opt|workspace|mnt|srv|data|usr|etc)/[^\\s'\")`]*")

// scrub takes the paths of the worker out of a text that is going to an issue, which may be
// public, and where the layout of the state of the service is nobody's business: the directories
// of the attempt, the temporary directory and the home directory, and any other path of a
// machine.
func (a *attempt) scrub(text string) string {
	dirs := []string{a.cloneDir, filepath.Join(a.req.Dir, inputDirName), a.req.Dir, os.TempDir()}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, home)
	}
	for _, dir := range dirs {
		if len(dir) > 1 {
			text = strings.ReplaceAll(text, dir, "<worker>")
		}
	}
	return pathPattern.ReplaceAllString(text, "${1}<worker path>")
}
