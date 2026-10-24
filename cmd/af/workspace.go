package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"

	"github.com/agent-fox-dev/agentfox/internal/afclient"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// workspaceListDoc is what af reads of the answer to GET /api/v1/workspaces: the subscriptions, each of
// them carried as the service sent it.
type workspaceListDoc struct {
	Workspaces []json.RawMessage `json:"workspaces"`
}

// subscriptionDoc is the subscription view of the service, with the fields af names; the view is carried
// as it was received, so that fields it gains reach the result.
type subscriptionDoc struct {
	Slug         string  `json:"slug"`
	TokenUser    *string `json:"token_user"`
	SubscribedBy string  `json:"subscribed_by"`
	TokenState   string  `json:"token_state"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// slugResult is the result of af workspace unsubscribe.
type slugResult struct {
	Slug string `json:"slug"`
}

// slugArg is the slug that a workspace command works on, its first positional argument.
func slugArg(r *toolio.Run) (string, error) {
	if len(r.Args) == 0 {
		return "", &toolio.Failure{Category: toolio.CategoryUsage, Message: "give the slug of the workspace"}
	}
	return r.Args[0], nil
}

// workspacePath is the API path of the workspace slug, which the service checks.
func workspacePath(slug string) string { return "/workspaces/" + url.PathEscape(slug) }

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
	var doc workspaceListDoc
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

// registerWorkspaceSubscribe registers af workspace subscribe.
func registerWorkspaceSubscribe(app *toolio.App) {
	var hubTokenFile string
	var server serverFlag
	app.Register(toolio.Command{
		Path:    "workspace subscribe",
		Summary: "Subscribe a hub workspace on the agent-fox service, or replace its subscription",
		Usage:   "af workspace subscribe <slug> --hub-token-file FILE|- [--server URL]",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&hubTokenFile, "hub-token-file", "", "the `file` that holds the personal access token of the workspace on hub; - reads stdin")
			server.bind(fs)
		},
		RawStdin:     true,
		Timeout:      true,
		ResultSchema: resultSchema("subscription"),
		Run: func(r *toolio.Run) (toolio.Outcome, error) {
			return workspaceSubscribe(r, app, hubTokenFile, &server)
		},
	})
}

// workspaceSubscribe is PUT /api/v1/workspaces/{slug} with the token of the workspace. The service answers 201 for a subscription and 200 for a
// replaced one, and both are success.
func workspaceSubscribe(r *toolio.Run, app *toolio.App, hubTokenFile string, server *serverFlag) (toolio.Outcome, error) {
	if hubTokenFile == "" {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--hub-token-file is required: name the file that holds the token of the workspace, or - to read it from stdin", FixHint: &toolio.FixHint{Flag: "--hub-token-file"}}
	}
	slug, err := slugArg(r)
	if err != nil {
		return toolio.Outcome{}, err
	}
	pat, err := afclient.ReadSecretFile("--hub-token-file", hubTokenFile, r.Stdin)
	if err != nil {
		return toolio.Outcome{}, secretFailure(err)
	}
	client, err := connect(r, app, server, false)
	if err != nil {
		return toolio.Outcome{}, err
	}
	r.Redact(pat)
	client.Redact(pat)
	body := map[string]string{"hub_token": pat}
	resp, err := client.Do(r.Ctx, http.MethodPut, workspacePath(slug), nil, body)
	if err != nil {
		return toolio.Outcome{}, requestFailure(err)
	}
	if err := resp.Decode(&subscriptionDoc{}); err != nil {
		return toolio.Outcome{}, requestFailure(err)
	}
	summary := "subscribed the workspace " + slug
	if resp.Status == http.StatusOK {
		summary = "replaced the subscription of the workspace " + slug
	}
	return toolio.Outcome{Status: toolio.StatusDone, Summary: summary, Result: json.RawMessage(resp.Body)}, nil
}

// registerWorkspaceGet registers af workspace get.
func registerWorkspaceGet(app *toolio.App) {
	var server serverFlag
	app.Register(toolio.Command{
		Path:         "workspace get",
		Summary:      "Show the subscription of a workspace on the agent-fox service",
		Usage:        "af workspace get <slug> [--server URL]",
		Flags:        server.bind,
		Timeout:      true,
		ResultSchema: resultSchema("subscription"),
		Run: func(r *toolio.Run) (toolio.Outcome, error) {
			slug, err := slugArg(r)
			if err != nil {
				return toolio.Outcome{}, err
			}
			client, err := connect(r, app, &server, false)
			if err != nil {
				return toolio.Outcome{}, err
			}
			resp, err := client.Do(r.Ctx, http.MethodGet, workspacePath(slug), nil, nil)
			if err != nil {
				return toolio.Outcome{}, requestFailure(err)
			}
			if err := resp.Decode(&subscriptionDoc{}); err != nil {
				return toolio.Outcome{}, requestFailure(err)
			}
			return toolio.Outcome{Status: toolio.StatusDone, Summary: "the workspace " + slug + " is subscribed", Result: json.RawMessage(resp.Body)}, nil
		},
	})
}

// registerWorkspaceUnsubscribe registers af workspace unsubscribe.
func registerWorkspaceUnsubscribe(app *toolio.App) {
	var server serverFlag
	app.Register(toolio.Command{
		Path:         "workspace unsubscribe",
		Summary:      "Remove the subscription of a workspace on the agent-fox service, cancelling its open jobs",
		Usage:        "af workspace unsubscribe <slug> [--server URL]",
		Flags:        server.bind,
		Timeout:      true,
		ResultSchema: resultSchema("slug"),
		Run: func(r *toolio.Run) (toolio.Outcome, error) {
			slug, err := slugArg(r)
			if err != nil {
				return toolio.Outcome{}, err
			}
			client, err := connect(r, app, &server, false)
			if err != nil {
				return toolio.Outcome{}, err
			}
			if _, err := client.Do(r.Ctx, http.MethodDelete, workspacePath(slug), nil, nil); err != nil {
				return toolio.Outcome{}, requestFailure(err)
			}
			return toolio.Outcome{
				Status:  toolio.StatusDone,
				Summary: "unsubscribed the workspace " + slug + "; its open jobs were cancelled",
				Result:  slugResult{Slug: slug},
			}, nil
		},
	})
}
