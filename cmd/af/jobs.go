package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"strconv"

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
func echoEvent(r *toolio.Run, ev afclient.Event) {
	var line bytes.Buffer
	if err := json.Compact(&line, ev.Document); err != nil {
		return
	}
	line.WriteByte('\n')
	_, _ = r.Stderr.Write(line.Bytes())
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
