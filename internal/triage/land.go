package triage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
)

// The actions of a landing.
const (
	ActionReplacedBody = "replaced_body"
	ActionCommented    = "commented"
	ActionCreatedIssue = "created_issue"
	ActionPlanned      = "planned"
	ActionNone         = "none"
)

// The warnings of a landing.
const (
	WarnReportChanged = "report_changed_during_triage"
	WarnBodyTooLong   = "body_too_long"
	WarnLabelsFailed  = "labels_failed"
)

// More categories of a refusal.
const (
	CategoryNoResult = "no_result"
	CategoryIO       = "io"
)

// The states of a side effect.
const (
	StateDone    = "done"
	StatePlanned = "planned"
	StateFailed  = "failed"
)

// Facts are the facts of a run that triage.json keeps.
type Facts struct {
	FormatVersion int        `json:"format_version"`
	Input         InputFact  `json:"input"`
	Target        RepoFact   `json:"target"`
	Issue         *IssueFact `json:"issue"`
	// OriginalReportSHA256 is the hash of the original report as the brief had it.
	OriginalReportSHA256 string     `json:"original_report_sha256"`
	Prefix               PrefixFact `json:"prefix"`
}

// InputFact says what was triaged.
type InputFact struct {
	Kind        string `json:"kind"`
	Origin      string `json:"origin"`
	Fingerprint string `json:"fingerprint"`
}

// RepoFact is a repository, with its host when it is known.
type RepoFact struct {
	Repo string `json:"repo"`
	Host string `json:"host"`
}

// IssueFact is an issue.
type IssueFact struct {
	Repo   string `json:"repo"`
	Host   string `json:"host"`
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// PrefixFact is the size and hash of the prefix of the brief.
type PrefixFact struct {
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Artifact is something a landing produced.
type Artifact struct {
	Kind   string
	Path   string
	URL    string
	Number int
}

// SideEffect is a write a landing made, or would have.
type SideEffect struct {
	Kind, Target, State string
}

// LandParams is what a landing is made of. The callbacks may be nil.
type LandParams struct {
	// RunDir is the run directory on the host, RunID its id.
	RunDir string
	RunID  string
	Facts  Facts
	Forge  issuex.Client
	// Mode is LandIssue or LandNone; Labels are the labels to add.
	Mode   string
	Labels []string
	// IsDryRun says whether an error is the refusal of a write by a dry run, which
	// the forge it is given then stands in for.
	IsDryRun func(error) bool
	// Tree is what the submission is validated against again.
	Tree Tree
	// Created is the issue an earlier landing of this run created, as that landing
	// reported it, if any.
	Created func() (IssueInfo, bool)
	// Check, when it is not nil, is asked before anything is written: it is the
	// target and the credential of a landing that the caller has not checked
	// already. It is asked after the submission is found to carry no question,
	// since a question lands nothing and needs neither.
	Check func(ctx context.Context) *Refusal

	Record     func(kind string, data any) error
	Warn       func(code, message string)
	Artifact   func(Artifact)
	SideEffect func(SideEffect)
}

// IssueInfo is the issue a landing wrote to.
type IssueInfo struct {
	Repo   string `json:"repo"`
	Host   string `json:"host"`
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// LandResult is what a landing came to.
type LandResult struct {
	Action    string
	Diagnosis Diagnosis
	Issue     *IssueInfo
	Labels    []string
	// OriginalSHA256 is the hash of the original report the body holds.
	OriginalSHA256 string
	// Question is set when the submission carries one: nothing was landed.
	Question *Question
	// Submitted says there was a valid submission, whatever became of the landing.
	Submitted bool
	// DiagnosisPath is diagnosis.md, when it was written.
	DiagnosisPath string
}

// Land lands the submission of a run: it re-validates it against the tree, writes
// the rendered body to diagnosis.md, and, unless the mode is none, writes it to
// the forge. For an issue it replaces the body and keeps the person's original
// report as the issue has it now; when the body would be too long for the forge it
// posts the diagnosis as a comment instead. For any other report it creates an
// issue, or, when an earlier landing of the run created one, replaces that
// issue's body. A landing can be repeated: it writes the same bytes again.
//
// The error is a *Refusal: no_result for a run with no valid submission, and
// auth or forge for a write that failed. A submission that carries a question
// lands nothing and is handed back in the result. How many calls the brain had
// refused is not read: it does not decide anything here.
func Land(ctx context.Context, p LandParams) (LandResult, error) {
	res := LandResult{Action: ActionNone}
	d, refusal := p.loadSubmission()
	if refusal != nil {
		return res, refusal
	}
	res.Diagnosis, res.Submitted = d, true
	if d.Question != nil {
		res.Question = d.Question
		return res, nil
	}

	if p.Check != nil && p.Mode != LandNone {
		if refusal := p.Check(ctx); refusal != nil {
			return res, refusal
		}
	}
	original, comments, refusal := p.original(ctx)
	if refusal != nil {
		return res, refusal
	}
	res.OriginalSHA256 = sha256Hex(original)
	body := Render(d, p.RunID, original)

	path := filepath.Join(p.RunDir, "diagnosis.md")
	if err := agentdir.WriteFileAtomic(path, []byte(body)); err != nil {
		return res, &Refusal{Category: CategoryIO, Message: fmt.Sprintf("cannot write %s: %v", path, err)}
	}
	res.DiagnosisPath = path
	p.artifact(Artifact{Kind: "diagnosis", Path: path})

	if p.Mode == LandNone {
		p.record(res)
		return res, nil
	}
	var err *Refusal
	if p.Facts.Input.Kind == KindIssue && p.Facts.Issue != nil {
		err = p.landOnIssue(ctx, d, body, comments, &res)
	} else {
		err = p.landAsNewIssue(ctx, d, body, &res)
	}
	if err != nil {
		return res, err
	}
	p.record(res)
	return res, nil
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (p LandParams) warn(code, message string) {
	if p.Warn != nil {
		p.Warn(code, message)
	}
}

func (p LandParams) artifact(a Artifact) {
	if p.Artifact != nil {
		p.Artifact(a)
	}
}

func (p LandParams) effect(kind, target, state string) {
	if p.SideEffect != nil {
		p.SideEffect(SideEffect{Kind: kind, Target: target, State: state})
	}
}

// record notes the end of the landing in the ledger.
func (p LandParams) record(res LandResult) {
	if p.Record == nil {
		return
	}
	labels := res.Labels
	if labels == nil {
		labels = []string{}
	}
	_ = p.Record("triage.land", map[string]any{"action": res.Action, "issue": res.Issue, "labels": labels}) // the callback reports a ledger it cannot write; the landing is done either way
}

// loadSubmission reads the submission of the run and validates its payload again
// against the tree. A run that has none that passes has no result.
func (p LandParams) loadSubmission() (Diagnosis, *Refusal) {
	noResult := func(format string, args ...any) (Diagnosis, *Refusal) {
		return Diagnosis{}, &Refusal{Category: CategoryNoResult, Message: fmt.Sprintf(format, args...)}
	}
	data, err := os.ReadFile(filepath.Join(p.RunDir, "submission.json"))
	if err != nil {
		return noResult("run %s has no submission: %v", p.RunID, err)
	}
	var file struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &file); err != nil || len(file.Payload) == 0 {
		return noResult("the submission of run %s is not a submission file", p.RunID)
	}
	d, problems := Validate(file.Payload, p.Tree)
	if len(problems) > 0 {
		return noResult("the submission of run %s does not pass validation against the repository as it is now: %s", p.RunID, problems[0])
	}
	return d, nil
}

// original is the person's original report the body is rendered around. For an
// issue that is to be written to, it is the report as the issue has it now,
// with a warning when that is not what the brief had; otherwise it is the
// report as the brief had it, from original.md. The comments are those of the
// issue as it is now, for a landing that has to know whether its own is there.
func (p LandParams) original(ctx context.Context) (string, []string, *Refusal) {
	if p.Mode != LandNone && p.Facts.Input.Kind == KindIssue && p.Facts.Issue != nil {
		ref := p.issueRef()
		thread, err := p.Forge.ReadIssue(ctx, ref)
		if err != nil {
			return "", nil, p.refuse(fmt.Sprintf("read %s", ref.URL()), ref.Repo, err)
		}
		current, _, warning := Extract(thread.Issue.Body)
		if warning != "" {
			p.warn(warning, unparsedMessage(ref))
		}
		if sha256Hex(current) != p.Facts.OriginalReportSHA256 {
			p.warn(WarnReportChanged, fmt.Sprintf("the report in %s changed while the run was going; the diagnosis may predate the change, and the current report is kept", ref.URL()))
		}
		comments := make([]string, len(thread.Comments))
		for i, c := range thread.Comments {
			comments[i] = c.Body
		}
		return current, comments, nil
	}
	data, err := os.ReadFile(filepath.Join(p.RunDir, "original.md"))
	if err != nil {
		return "", nil, &Refusal{Category: CategoryIO, Message: fmt.Sprintf("cannot read the original report of run %s: %v", p.RunID, err)}
	}
	return string(data), nil, nil
}

// unparsedMessage is the warning text of a body that starts with the triage
// marker and whose original report cannot be found.
func unparsedMessage(ref issuex.IssueRef) string {
	return fmt.Sprintf("the body of %s starts with the triage marker but its original report cannot be found: the whole body is taken as the report", ref)
}

// issueRef is the issue of the run.
func (p LandParams) issueRef() issuex.IssueRef {
	return issuex.IssueRef{Repo: repoOf(p.Facts.Issue.Repo, p.Facts.Issue.Host), Number: p.Facts.Issue.Number}
}

// repoOf is the repository owner/name on host.
func repoOf(name, host string) issuex.Repo {
	r, _ := issuex.ParseRepo(name)
	r.Host = host
	return r
}

// write sends one write, and tells how it went: a refusal by a dry run is a
// plan, which the dry run has recorded as a side effect already, and any other
// error is a failure.
func (p LandParams) write(kind, target string, do func() error) (planned bool, err error) {
	err = do()
	switch {
	case err == nil:
		p.effect(kind, target, StateDone)
		return false, nil
	case p.IsDryRun != nil && p.IsDryRun(err):
		return true, nil
	}
	p.effect(kind, target, StateFailed)
	return false, err
}

// refuse is the refusal for a forge error: no credential, or one that was
// refused, is auth; a rate limit or a lost connection is forge and may be tried
// again; anything else the forge answered is forge and may not.
func (p LandParams) refuse(what string, repo issuex.Repo, err error) *Refusal {
	c := LandCheck{Forge: p.Forge, Target: repo}
	msg := fmt.Sprintf("cannot %s: %v", what, err)
	var he *issuex.HTTPError
	switch {
	case issuex.IsNoToken(err), isUnauthorized(err):
		return &Refusal{Category: CategoryAuth, Message: msg, FixHintEnv: tokenVariable(c)}
	case issuex.IsRateLimited(err):
		return &Refusal{Category: CategoryForge, Message: msg, Retryable: true}
	case errors.As(err, &he), errors.Is(err, issuex.ErrNotFound), errors.Is(err, issuex.ErrConflict):
		return &Refusal{Category: CategoryForge, Message: msg}
	}
	return &Refusal{Category: CategoryForge, Message: msg, Retryable: true}
}

// landOnIssue writes the diagnosis to the issue of the run.
func (p LandParams) landOnIssue(ctx context.Context, d Diagnosis, body string, comments []string, res *LandResult) *Refusal {
	ref := p.issueRef()
	info := &IssueInfo{Repo: ref.Repo.String(), Host: ref.Repo.Host, Number: ref.Number, URL: p.Facts.Issue.URL}

	var planned bool
	var err error
	if len(body) <= issuex.MaxBodyLength(p.Forge) {
		planned, err = p.write("update_issue", ref.String(), func() error {
			_, err := p.Forge.UpdateIssue(ctx, ref, issuex.UpdateIssueRequest{Body: body})
			return err
		})
		res.Action = ActionReplacedBody
	} else {
		p.warn(WarnBodyTooLong, fmt.Sprintf("the body of %s with the diagnosis would be %d bytes, more than the %d the forge takes: the diagnosis is posted as a comment, and the body is left as it is", ref.URL(), len(body), issuex.MaxBodyLength(p.Forge)))
		comment := RenderDiagnosisOnly(d, p.RunID)
		if !slices.ContainsFunc(comments, func(c string) bool { return sameText(c, comment) }) {
			planned, err = p.write("add_comment", ref.String(), func() error {
				_, err := p.Forge.AddComment(ctx, ref, comment)
				return err
			})
		} // else a landing of this run posted it already, and landing is repeatable
		res.Action = ActionCommented
	}
	if err != nil {
		res.Action = ActionNone
		return p.refuse(fmt.Sprintf("write to %s", ref.URL()), ref.Repo, err)
	}
	res.Issue = info
	if planned {
		res.Action = ActionPlanned
	} else {
		p.artifact(Artifact{Kind: "issue", URL: info.URL, Number: info.Number})
	}
	p.addLabels(ctx, ref, res)
	return nil
}

// addLabels adds the labels of the landing to the issue. A label that cannot be
// added is a warning: the body is the deliverable.
func (p LandParams) addLabels(ctx context.Context, ref issuex.IssueRef, res *LandResult) {
	if len(p.Labels) == 0 {
		return
	}
	planned, err := p.write("add_labels", ref.String(), func() error { return p.Forge.AddLabels(ctx, ref, p.Labels) })
	switch {
	case err != nil:
		p.warn(WarnLabelsFailed, fmt.Sprintf("the labels %v could not be added to %s: %v", p.Labels, ref.URL(), err))
	case !planned:
		res.Labels = append([]string(nil), p.Labels...)
	}
}

// landAsNewIssue creates an issue for a report that is no issue, in the
// repository the run recorded, or, if an earlier landing created one, replaces
// its body.
func (p LandParams) landAsNewIssue(ctx context.Context, d Diagnosis, body string, res *LandResult) *Refusal {
	target := repoOf(p.Facts.Target.Repo, p.Facts.Target.Host)
	if limit := issuex.MaxBodyLength(p.Forge); len(body) > limit {
		return &Refusal{Category: CategoryUsage, Message: fmt.Sprintf("the issue would be %d bytes, and %s takes %d: the report cannot land whole; the diagnosis is in %s",
			len(body), target, limit, res.DiagnosisPath)}
	}

	if p.Created != nil {
		if info, ok := p.Created(); ok {
			ref := issuex.IssueRef{Repo: repoOf(info.Repo, target.Host), Number: info.Number}
			planned, err := p.write("update_issue", ref.String(), func() error {
				_, err := p.Forge.UpdateIssue(ctx, ref, issuex.UpdateIssueRequest{Body: body})
				return err
			})
			if err != nil {
				return p.refuse(fmt.Sprintf("write to %s", ref.String()), ref.Repo, err)
			}
			res.Action = ActionReplacedBody
			res.Issue = &IssueInfo{Repo: info.Repo, Host: firstNonEmpty(info.Host, hostOf(info.URL)), Number: info.Number, URL: info.URL}
			if planned {
				res.Action = ActionPlanned
			} else {
				p.artifact(Artifact{Kind: "issue", URL: res.Issue.URL, Number: ref.Number})
			}
			p.addLabels(ctx, ref, res)
			return nil
		}
	}

	var created issuex.Issue
	planned, err := p.write("create_issue", target.String(), func() error {
		var err error
		created, err = p.Forge.CreateIssue(ctx, target, issuex.CreateIssueRequest{Title: d.Title, Body: body, Labels: p.Labels})
		return err
	})
	if err != nil {
		return p.refuse(fmt.Sprintf("create an issue in %s", target), target, err)
	}
	if planned {
		res.Action = ActionPlanned
		return nil
	}
	url := created.HTMLURL
	if url == "" {
		url = created.URL
	}
	res.Action = ActionCreatedIssue
	res.Issue = &IssueInfo{Repo: target.String(), Host: firstNonEmpty(target.Host, hostOf(url)), Number: created.Number, URL: url}
	res.Labels = append([]string(nil), p.Labels...)
	p.artifact(Artifact{Kind: "issue", URL: url, Number: created.Number})
	return nil
}

// sameText says whether two texts are the same to a person: a forge keeps a
// comment as it was sent, except for the line endings and the white space at
// its ends.
func sameText(a, b string) bool {
	norm := func(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")) }
	return norm(a) == norm(b)
}

// hostOf is the host of a URL, or empty when it is none.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
