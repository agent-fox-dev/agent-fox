package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// maxQueueRead bounds what is read from stdin or a file before the content is trimmed and cut: a
// guard on memory, far above toolio.MaxInputBytes.
const maxQueueRead = 16 << 20

var (
	// positiveInt is an issue number, with or without the # in front of it.
	positiveInt = regexp.MustCompile(`^#?[0-9]+$`)
	// repoIssue is owner/repo#N: two or more segments of [A-Za-z0-9_.-] and a positive number.
	repoIssue = regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+#[1-9][0-9]*$`)
)

// queueCommand is the service mode of af triage or af fix (name): it queues a job on a workspace
// of the agent-fox service and, with --wait, follows it. No loop runs on this machine, and no forge
// client or run directory is made.
func queueCommand(app *toolio.App, name string) *toolio.Command {
	var workspace string
	var wait bool
	var server serverFlag
	usage := "af " + name + " <input> --workspace <slug> [--wait] [--context TEXT] [--server URL]"
	return &toolio.Command{
		Path:    name,
		Summary: "Queue a " + name + " job on a workspace of the agent-fox service",
		Usage:   usage,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&workspace, "workspace", "", "the `slug` of the workspace that the job is queued on")
			fs.BoolVar(&wait, "wait", false, "follow the job until it is at rest, and end with what it came to")
			server.bind(fs)
		},
		RawStdin:     true,
		Context:      true,
		Timeout:      true,
		ResultSchema: resultSchema("job_result"),
		Run:          func(r *toolio.Run) (toolio.Outcome, error) { return queueJob(r, app, name, workspace, wait, &server) },
	}
}

// queueJob posts the input to the route of the workspace and ends with the job it made. Its checks
// run in this order, and none sends a request: the flags, the input, the server, the credential.
func queueJob(r *toolio.Run, app *toolio.App, name, workspace string, wait bool, server *serverFlag) (toolio.Outcome, error) {
	if workspace == "" {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--workspace is empty: give the slug of a workspace", FixHint: &toolio.FixHint{Flag: "--workspace"}}
	}
	if len(r.Args) == 0 {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "no input given: pass an issue, text, a file, or - to read stdin"}
	}
	body, err := queueBody(r, name, r.Args[0])
	if err != nil {
		return toolio.Outcome{}, err
	}
	if len(r.Context) > 0 {
		body["context"] = r.Context
	}
	client, err := connect(r, app, server, true)
	if err != nil {
		return toolio.Outcome{}, err
	}

	resp, err := client.Do(r.Ctx, http.MethodPost, workspacePath(workspace)+"/"+name, nil, body)
	if err != nil {
		return toolio.Outcome{}, requestFailure(err)
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := resp.Decode(&job); err != nil {
		return toolio.Outcome{}, requestFailure(err)
	}
	if job.ID == "" {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryService, Message: "the service queued the job and its document has no id"}
	}
	r.Artifact(jobArtifact(client.Server(), job.ID))
	if wait {
		return followJob(r, client, job.ID, 0, false)
	}
	r.Next(toolio.Next{Argv: withServer(client.Server(), "af", "jobs", "watch", job.ID), Reason: "follow the job until it is at rest"})
	return toolio.Outcome{
		Status:  toolio.StatusDone,
		Summary: fmt.Sprintf("queued the %s job %s on the workspace %s", name, job.ID, workspace),
		Result:  jobResult{Job: json.RawMessage(resp.Body), Envelope: nullEnvelope},
	}, nil
}

// queueBody turns the input into the fields of the request, by the first rule that matches, and
// tells the run what the input was. af fix takes only the issue forms.
func queueBody(r *toolio.Run, name, arg string) (map[string]any, error) {
	trimmed := strings.TrimSpace(arg)
	lower := strings.ToLower(trimmed)
	switch {
	case arg == "-":
		if name == "fix" {
			return nil, fixTakesAnIssue(arg)
		}
		data, err := io.ReadAll(io.LimitReader(r.Stdin, maxQueueRead))
		if err != nil {
			return nil, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot read stdin: %v", err)}
		}
		text, cut, err := cleanContent(r, string(data), "stdin")
		if err != nil {
			return nil, err
		}
		r.SetInput(toolio.KindStdin, "", cut)
		return map[string]any{"text": text}, nil
	case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
		r.SetInput(toolio.KindIssue, trimmed, false)
		return map[string]any{"issue": trimmed}, nil
	case positiveInt.MatchString(trimmed):
		if n, err := strconv.ParseInt(strings.TrimPrefix(trimmed, "#"), 10, 64); err == nil && n > 0 {
			r.SetInput(toolio.KindIssue, "", false)
			return map[string]any{"issue": n}, nil
		}
	case repoIssue.MatchString(trimmed):
		r.SetInput(toolio.KindIssue, "", false)
		return map[string]any{"issue": trimmed}, nil
	}
	if name == "fix" {
		return nil, fixTakesAnIssue(arg)
	}

	if info, err := os.Stat(arg); err == nil && info.IsDir() {
		return nil, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("%s is a directory; pass a file, text, an issue or - for stdin", arg)}
	} else if err == nil && info.Mode().IsRegular() {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return nil, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot resolve %s: %v", arg, err)}
		}
		f, err := os.Open(abs)
		if err != nil {
			return nil, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot read %s: %v", abs, err)}
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxQueueRead))
		if err != nil {
			return nil, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot read %s: %v", abs, err)}
		}
		content, cut, err := cleanContent(r, string(data), abs)
		if err != nil {
			return nil, err
		}
		r.SetInput(toolio.KindFile, abs, cut)
		return map[string]any{"file": map[string]string{"name": filepath.Base(abs), "content": content}}, nil
	}

	text, cut, err := cleanContent(r, arg, "the text")
	if err != nil {
		return nil, err
	}
	r.SetInput(toolio.KindText, "", cut)
	return map[string]any{"text": text}, nil
}

// fixTakesAnIssue is the usage failure for an input that af fix cannot queue.
func fixTakesAnIssue(arg string) error {
	return &toolio.Failure{
		Category: toolio.CategoryUsage,
		Message:  fmt.Sprintf("with --workspace, af fix takes an issue: N, #N, owner/repo#N or an issue URL; %.60q is none of them", arg),
	}
}

// cleanContent trims the content of a text or a file, which must not be empty, hold a NUL byte or
// be invalid UTF-8, and cuts it at a UTF-8 boundary so that it and the truncation marker fit in
// toolio.MaxInputBytes. It raises input_truncated when it cut. what names the source.
func cleanContent(r *toolio.Run, content, what string) (string, bool, error) {
	content = strings.TrimSpace(content)
	switch {
	case content == "":
		return "", false, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("the input from %s is empty", what)}
	case strings.IndexByte(content, 0) >= 0:
		return "", false, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("the input from %s holds a NUL byte, so it is not text", what)}
	case !utf8.ValidString(content):
		return "", false, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("the input from %s is not valid UTF-8", what)}
	}
	if len(content) <= toolio.MaxInputBytes {
		return content, false, nil
	}
	cut := toolio.MaxInputBytes - len(toolio.TruncationMarker)
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}
	r.Warn(toolio.WarnInputTruncated, fmt.Sprintf("the input was cut at %d bytes", toolio.MaxInputBytes))
	return content[:cut] + toolio.TruncationMarker, true, nil
}
