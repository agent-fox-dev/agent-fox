package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// registerWorkspaceList registers af workspace list.
func registerWorkspaceList(app *toolio.App) {
	var server serverFlag
	app.Register(toolio.Command{
		Path:         "workspace list",
		Summary:      "List the workspaces subscribed on the agent-fox service",
		Usage:        "af workspace list [--server URL]",
		Flags:        server.bind,
		MaxArgs:      toolio.NoArgs,
		Timeout:      true,
		ResultSchema: resultSchema("workspace_list"),
		Run:          func(r *toolio.Run) (toolio.Outcome, error) { return workspaceList(r, app, &server) },
	})
}

// workspaceList is GET /api/v1/workspaces: the subscriptions the caller sees, as the service
// answered.
func workspaceList(r *toolio.Run, app *toolio.App, server *serverFlag) (toolio.Outcome, error) {
	client, err := connect(r, app, server, false)
	if err != nil {
		return toolio.Outcome{}, err
	}
	resp, err := client.Do(r.Ctx, http.MethodGet, "/workspaces", nil, nil)
	if err != nil {
		return toolio.Outcome{}, requestFailure(err)
	}
	var doc struct {
		Workspaces []json.RawMessage `json:"workspaces"`
	}
	if err := resp.Decode(&doc); err != nil {
		return toolio.Outcome{}, requestFailure(err)
	}
	if doc.Workspaces == nil {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryService, Message: "the service answered with a document that is not a list of workspaces"}
	}
	summary := fmt.Sprintf("%d workspaces subscribed", len(doc.Workspaces))
	if len(doc.Workspaces) == 1 {
		summary = "1 workspace subscribed"
	}
	return toolio.Outcome{Status: toolio.StatusDone, Summary: summary, Result: json.RawMessage(resp.Body)}, nil
}
