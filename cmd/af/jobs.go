package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/afclient"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// jobResult is the result of the commands that read, act on or follow a job. Job is the document of
// the service as it was received, and Envelope the envelope of the final attempt, null unless
// the command followed the job until it was at rest and an attempt had finished.
type jobResult struct {
	Job      json.RawMessage `json:"job"`
	Envelope json.RawMessage `json:"envelope"`
}

// jobPageDoc is one page of jobs as the service answers it, each job carried as the service sent it.
type jobPageDoc struct {
	Jobs       []json.RawMessage `json:"jobs"`
	NextCursor *string           `json:"next_cursor"`
}

// nullEnvelope is the envelope of a result that has none.
var nullEnvelope = json.RawMessage("null")

// jobArtifact is the artifact that names the job id on the server.
func jobArtifact(srv afclient.Server, id string) toolio.Artifact {
	return toolio.Artifact{Kind: "job", Name: id, URL: srv.URL + "/api/v1/jobs/" + url.PathEscape(id)}
}

// withServer is argv with the server added to it when the server came from the flag, so that the
// suggestion runs against the same service when AF_URL is not set.
func withServer(srv afclient.Server, argv ...string) []string {
	if srv.FromFlag {
		argv = append(argv, "--server", srv.URL)
	}
	return argv
}

// registerJobsWatch registers af jobs watch.
func registerJobsWatch(app *toolio.App) {
	var since int64
	var server serverFlag
	app.Register(toolio.Command{
		Path:    "jobs watch",
		Summary: "Follow a job of the agent-fox service until it is at rest, writing its events to stderr",
		Usage:   "af jobs watch <id> [--since N] [--server URL]",
		Flags: func(fs *flag.FlagSet) {
			fs.Int64Var(&since, "since", 0, "deliver the events above this `number`, in place of all of them")
			server.bind(fs)
		},
		Timeout:      true,
		ResultSchema: resultSchema("job_result"),
		Run:          func(r *toolio.Run) (toolio.Outcome, error) { return jobsWatch(r, app, since, &server) },
	})
}

// jobsWatch follows the job to rest. Each event is emitted as job_event and written to stderr as one
// line of compact JSON, in the order of its number. The artifact of the job is recorded before
// the following starts.
func jobsWatch(r *toolio.Run, app *toolio.App, since int64, server *serverFlag) (toolio.Outcome, error) {
	if len(r.Args) == 0 {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "give the id of the job to follow"}
	}
	if since < 0 {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--since %d is negative; give the number of an event, or leave it out for all of them", since), FixHint: &toolio.FixHint{Flag: "--since"}}
	}
	id := r.Args[0]
	client, err := connect(r, app, server, false)
	if err != nil {
		return toolio.Outcome{}, err
	}
	r.Artifact(jobArtifact(client.Server(), id))

	return followJob(r, client, id, since, true)
}

// echoEvent writes an event of the job to stderr as one line of compact JSON.
func echoEvent(r *toolio.Run, ev afclient.Event, scrub func(string) string) {
	var line bytes.Buffer
	if err := json.Compact(&line, ev.Document); err != nil {
		return
	}
	line.WriteByte('\n')
	_, _ = io.WriteString(r.Stderr, scrub(line.String()))
}

// followFailure is the failure for a following that ended without the job at rest. A following that
// gave up suggests to watch the job again from the last event.
func followFailure(r *toolio.Run, srv afclient.Server, id string, err error) error {
	var gu *afclient.GaveUp
	if errors.As(err, &gu) {
		r.Next(toolio.Next{
			Argv:   withServer(srv, "af", "jobs", "watch", id, "--since", strconv.FormatInt(gu.LastSeq, 10)),
			Reason: "follow the job again from the last event; it may still be running",
		})
	}
	return requestFailure(err)
}

// jobPath is the API path of the job id.
func jobPath(id string) string { return "/jobs/" + url.PathEscape(id) }

// idArg is the id of the job that a jobs command works on, its first positional argument.
func idArg(r *toolio.Run) (string, error) {
	if len(r.Args) == 0 {
		return "", &toolio.Failure{Category: toolio.CategoryUsage, Message: "give the id of the job"}
	}
	return r.Args[0], nil
}

// jobDocOutcome is the result of a command that shows or acts on a job without following it:
// the job document as the service answered, and no envelope.
func jobDocOutcome(resp *afclient.Response, id, verb string) (toolio.Outcome, error) {
	var job struct {
		State string `json:"state"`
	}
	if err := resp.Decode(&job); err != nil {
		return toolio.Outcome{}, requestFailure(err)
	}
	return toolio.Outcome{
		Status:  toolio.StatusDone,
		Summary: fmt.Sprintf("%s the job %s, which is %s", verb, id, job.State),
		Result:  jobResult{Job: json.RawMessage(resp.Body), Envelope: nullEnvelope},
	}, nil
}

// registerJobsList registers af jobs list.
func registerJobsList(app *toolio.App) {
	var workspace, state, cursor givenString
	var server serverFlag
	app.Register(toolio.Command{
		Path:    "jobs list",
		Summary: "List one page of the jobs of the agent-fox service",
		Usage:   "af jobs list [--workspace S] [--state S] [--cursor C] [--server URL]",
		Flags: func(fs *flag.FlagSet) {
			workspace.bind(fs, "workspace", "list the jobs of this workspace only, by its `slug`")
			state.bind(fs, "state", "list the jobs in this `state` only")
			cursor.bind(fs, "cursor", "the `cursor` of the page, from the next of an earlier page")
			server.bind(fs)
		},
		MaxArgs:      toolio.NoArgs,
		Timeout:      true,
		ResultSchema: resultSchema("job_page"),
		Run: func(r *toolio.Run) (toolio.Outcome, error) {
			return jobsList(r, app, &workspace, &state, &cursor, &server)
		},
	})
}

// jobsList is GET /api/v1/workspaces/{S}/jobs with --workspace and GET /api/v1/jobs without, with
// --state and --cursor as the query when given. The service validates them. It gives one page, and
// next repeats the command with the cursor of the next one.
func jobsList(r *toolio.Run, app *toolio.App, workspace, state, cursor *givenString, server *serverFlag) (toolio.Outcome, error) {
	if workspace.given && workspace.value == "" {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--workspace is empty: give the slug of a workspace", FixHint: &toolio.FixHint{Flag: "--workspace"}}
	}
	client, err := connect(r, app, server, false)
	if err != nil {
		return toolio.Outcome{}, err
	}
	path := "/jobs"
	if workspace.given {
		path = workspacePath(workspace.value) + "/jobs"
	}
	query := url.Values{}
	if state.given {
		query.Set("state", state.value)
	}
	if cursor.given {
		query.Set("cursor", cursor.value)
	}
	resp, err := client.Do(r.Ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return toolio.Outcome{}, requestFailure(err)
	}
	var page jobPageDoc
	if err := resp.Decode(&page); err != nil {
		return toolio.Outcome{}, requestFailure(err)
	}
	if page.Jobs == nil {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryService, Message: "the service answered with a document that is not a page of jobs"}
	}
	summary := fmt.Sprintf("%d jobs", len(page.Jobs))
	if len(page.Jobs) == 1 {
		summary = "1 job"
	}
	if page.NextCursor != nil && *page.NextCursor != "" {
		argv := []string{"af", "jobs", "list"}
		if workspace.given {
			argv = append(argv, "--workspace", workspace.value)
		}
		if state.given {
			argv = append(argv, "--state", state.value)
		}
		argv = append(withServer(client.Server(), argv...), "--cursor", *page.NextCursor)
		r.Next(toolio.Next{Argv: argv, Reason: "list the next page of jobs"})
		summary += "; there are more"
	}
	return toolio.Outcome{Status: toolio.StatusDone, Summary: summary, Result: json.RawMessage(resp.Body)}, nil
}

// registerJobsGet registers af jobs get.
func registerJobsGet(app *toolio.App) {
	var server serverFlag
	app.Register(toolio.Command{
		Path:         "jobs get",
		Summary:      "Show a job of the agent-fox service with its attempts",
		Usage:        "af jobs get <id> [--server URL]",
		Flags:        server.bind,
		Timeout:      true,
		ResultSchema: resultSchema("job_result"),
		Run: func(r *toolio.Run) (toolio.Outcome, error) {
			return jobAction(r, app, &server, http.MethodGet, "", nil, "showed")
		},
	})
}

// registerJobsCancel registers af jobs cancel.
func registerJobsCancel(app *toolio.App) {
	var server serverFlag
	app.Register(toolio.Command{
		Path:         "jobs cancel",
		Summary:      "Cancel a job of the agent-fox service; a cancel takes effect at once",
		Usage:        "af jobs cancel <id> [--server URL]",
		Flags:        server.bind,
		Timeout:      true,
		ResultSchema: resultSchema("job_result"),
		Run: func(r *toolio.Run) (toolio.Outcome, error) {
			return jobAction(r, app, &server, http.MethodPost, "/cancel", nil, "cancelled")
		},
	})
}

// jobAction sends method to the job of the first argument, at the path below it with the body,
// and gives the job document of the answer.
func jobAction(r *toolio.Run, app *toolio.App, server *serverFlag, method, below string, body any, verb string) (toolio.Outcome, error) {
	id, err := idArg(r)
	if err != nil {
		return toolio.Outcome{}, err
	}
	client, err := connect(r, app, server, false)
	if err != nil {
		return toolio.Outcome{}, err
	}
	resp, err := client.Do(r.Ctx, method, jobPath(id)+below, nil, body)
	if err != nil {
		return toolio.Outcome{}, requestFailure(err)
	}
	return jobDocOutcome(resp, id, verb)
}

// registerJobsRetry registers af jobs retry.
func registerJobsRetry(app *toolio.App) {
	var wait bool
	var server serverFlag
	app.Register(toolio.Command{
		Path:    "jobs retry",
		Summary: "Ask the agent-fox service for another attempt at a job that failed",
		Usage:   "af jobs retry <id> [--wait] [--server URL]",
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&wait, "wait", false, "follow the job until it is at rest, and end with what it came to")
			server.bind(fs)
		},
		Timeout:      true,
		ResultSchema: resultSchema("job_result"),
		Run: func(r *toolio.Run) (toolio.Outcome, error) {
			id, err := idArg(r)
			if err != nil {
				return toolio.Outcome{}, err
			}
			client, err := connect(r, app, &server, false)
			if err != nil {
				return toolio.Outcome{}, err
			}
			return actAndMaybeWait(r, client, id, "/retry", map[string]any{}, "queued another attempt of", wait)
		},
	})
}

// actAndMaybeWait posts body to the path below the job id, and ends with the job document of the
// answer, or, with wait, records the job and follows it to rest.
func actAndMaybeWait(r *toolio.Run, client *afclient.Client, id, below string, body any, verb string, wait bool) (toolio.Outcome, error) {
	resp, err := client.Do(r.Ctx, http.MethodPost, jobPath(id)+below, nil, body)
	if err != nil {
		return toolio.Outcome{}, requestFailure(err)
	}
	if wait {
		r.Artifact(jobArtifact(client.Server(), id))
		return followJob(r, client, id, 0, false)
	}
	return jobDocOutcome(resp, id, verb)
}

// registerJobsAnswer registers af jobs answer.
func registerJobsAnswer(app *toolio.App) {
	var option givenString
	var wait bool
	var server serverFlag
	app.Register(toolio.Command{
		Path:    "jobs answer",
		Summary: "Answer the question of a blocked job of the agent-fox service",
		Usage:   "af jobs answer <id> (<text> | --context <text> | --option <id>) [--wait] [--server URL]",
		Flags: func(fs *flag.FlagSet) {
			option.bind(fs, "option", "answer with the option of this `id`, A, B, ...")
			fs.BoolVar(&wait, "wait", false, "follow the job until it is at rest, and end with what it came to")
			server.bind(fs)
		},
		MaxArgs:      2,
		Context:      true,
		Timeout:      true,
		ResultSchema: resultSchema("job_result"),
		Run:          func(r *toolio.Run) (toolio.Outcome, error) { return jobsAnswer(r, app, &option, wait, &server) },
	})
}

// jobsAnswer is POST /api/v1/jobs/{id}/answer with exactly one answer: positional text, --context
// or --option. An option is uppercased and sent as it is. A text is trimmed, must not be empty, and
// is sent as the option of the blocked question whose id it equals, ignoring case, else as the
// answer; af reads the job to find out.
func jobsAnswer(r *toolio.Run, app *toolio.App, option *givenString, wait bool, server *serverFlag) (toolio.Outcome, error) {
	id, err := idArg(r)
	if err != nil {
		return toolio.Outcome{}, err
	}
	answers := len(r.Context) + len(r.Args) - 1
	if option.given {
		answers++
	}
	if answers != 1 {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("give exactly one answer: text, --context TEXT or --option ID; got %d", answers)}
	}
	var text string
	body := map[string]string{}
	switch {
	case option.given:
		opt := strings.ToUpper(strings.TrimSpace(option.value))
		if opt == "" {
			return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--option is empty: give the id of an option, A, B, ...", FixHint: &toolio.FixHint{Flag: "--option"}}
		}
		body["option"] = opt
	case len(r.Context) == 1:
		text = strings.TrimSpace(r.Context[0])
	default:
		text = strings.TrimSpace(r.Args[1])
	}
	if !option.given && text == "" {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "the answer is empty: give some text, or --option ID"}
	}

	client, err := connect(r, app, server, false)
	if err != nil {
		return toolio.Outcome{}, err
	}
	if !option.given {
		body["answer"] = text
		resp, err := client.Do(r.Ctx, http.MethodGet, jobPath(id), nil, nil)
		if err != nil {
			return toolio.Outcome{}, requestFailure(err)
		}
		var job struct {
			Blocked *struct {
				Options []struct {
					ID string `json:"id"`
				} `json:"options"`
			} `json:"blocked"`
		}
		if err := resp.Decode(&job); err != nil {
			return toolio.Outcome{}, requestFailure(err)
		}
		if job.Blocked != nil {
			for _, o := range job.Blocked.Options {
				if strings.EqualFold(o.ID, text) {
					body = map[string]string{"option": o.ID}
					break
				}
			}
		}
	}
	return actAndMaybeWait(r, client, id, "/answer", body, "answered", wait)
}
