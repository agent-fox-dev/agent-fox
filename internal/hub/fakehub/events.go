package fakehub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// The types of the events of the forge that the fake hub sends, as hub names them.
const (
	EventIssueLabeled = "hub.forge.issue.labeled"
	EventIssueClosed  = "hub.forge.issue.closed"
)

const eventsPath = "/api/v1/events"

// HubEvent is an event the fake hub sends on its event stream, in hub's own shape: an audit event of the workspace whose
// resource is the issue, with the number and the label in its metadata.
type HubEvent struct {
	ID, Type string
	// Workspace is the slug the event names; the slug it is sent to when empty.
	Workspace string
	Issue     int
	Label     string
}

// eventConn is an open connection to the events route.
type eventConn struct {
	slug   string
	frames chan string
	end    chan struct{}
}

// eventStream serves GET /api/v1/events?workspace=<slug>: the access of the token to the workspace is checked as on the other
// routes, and the connection is then held open as text/event-stream, with what a test sends on it, until the client goes
// or a test ends it. Faults on the path are applied before, as on every route.
func (h *Hub) eventStream(w http.ResponseWriter, r *http.Request, token string) {
	slug := r.URL.Query().Get("workspace")
	if h.refuse(w, token, slug) {
		return
	}
	c := &eventConn{slug: slug, frames: make(chan string, 256), end: make(chan struct{})}
	h.mu.Lock()
	if h.eventConns == nil {
		h.eventConns = map[*eventConn]bool{}
	}
	h.eventConns[c] = true
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.eventConns, c)
		h.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	for {
		select {
		case f := <-c.frames:
			if _, err := w.Write([]byte(f)); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-c.end:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// broadcast queues the frame on every open connection of the slug; a connection whose queue is full drops it, as hub
// drops the oldest events of a client that does not read. The caller holds the lock.
func (h *Hub) broadcast(slug, frame string) {
	for c := range h.eventConns {
		if c.slug != slug {
			continue
		}
		select {
		case c.frames <- frame:
		default:
		}
	}
}

// eventFrame is the frame of the event, as hub writes an audit event.
func eventFrame(slug string, e HubEvent) string {
	ws := e.Workspace
	if ws == "" {
		ws = slug
	}
	meta := map[string]any{}
	if e.Issue != 0 {
		meta["number"] = e.Issue
	}
	if e.Label != "" {
		meta["label"] = e.Label
	}
	obj := map[string]any{
		"id": e.ID, "event_type": e.Type, "actor_id": "", "actor_type": "forge", "resource_type": "issue",
		"resource_id": strconv.Itoa(e.Issue), "action": "", "workspace": ws, "metadata": meta, "timestamp": stamp(time.Now()),
	}
	data, _ := json.Marshal(obj)
	return "event: audit_event\ndata: " + string(data) + "\n\n"
}

// SendEvent sends the event to every open connection of the slug's events route.
func (h *Hub) SendEvent(slug string, e HubEvent) { h.SendFrame(slug, eventFrame(slug, e)) }

// SendFrame sends the text, which must end a frame or a comment with an empty line, to every open connection of the slug's
// events route as it is: for a frame in another encoding, or a malformed one.
func (h *Hub) SendFrame(slug, frame string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broadcast(slug, frame)
}

// Heartbeat sends hub's heartbeat frame to every open connection of the slug's events route.
func (h *Hub) Heartbeat(slug string) {
	h.SendFrame(slug, fmt.Sprintf("event: heartbeat\ndata: {\"timestamp\":%q}\n\n", stamp(time.Now())))
}

// Comment sends a comment line to every open connection of the slug's events route.
func (h *Hub) Comment(slug string) { h.SendFrame(slug, ": ping\n\n") }

// EndEvents ends every open connection of the slug's events route, as hub does when it goes away.
func (h *Hub) EndEvents(slug string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.eventConns {
		if c.slug == slug {
			close(c.end)
			delete(h.eventConns, c)
		}
	}
}

// EventConns is how many connections of the slug's events route are open.
func (h *Hub) EventConns(slug string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for c := range h.eventConns {
		if c.slug == slug {
			n++
		}
	}
	return n
}

// EventsRequests are the requests of the events route, of every workspace, in order, those that failed included.
func (h *Hub) EventsRequests() []Request { return h.RequestsTo(http.MethodGet, eventsPath) }

// EmitLabeledOnAdd makes every label that is added to an issue through the forge API send hub.forge.issue.labeled to the
// open connections of the workspace, as hub's webhook ingress relays the forge's report of it, the worker's own writes
// included.
func (h *Hub) EmitLabeledOnAdd(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.emitLabeled = on
}

// labeled sends the event of a label added through the forge API, when that is asked for. The caller holds the lock.
func (h *Hub) labeled(slug string, issue int, label string) {
	if !h.emitLabeled {
		return
	}
	h.nextID++
	h.broadcast(slug, eventFrame(slug, HubEvent{ID: fmt.Sprintf("evt-auto-%d", h.nextID), Type: EventIssueLabeled, Issue: issue, Label: label}))
}

// SetIssueState sets the state of the issue, open or closed, as a person would.
func (h *Hub) SetIssueState(slug string, n int, state string) {
	h.update(slug, func(ws *workspace) {
		if issue := ws.issues[n]; issue != nil {
			issue.State = state
		}
	})
}
