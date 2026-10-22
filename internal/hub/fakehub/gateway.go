package fakehub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/gatewayd"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"
)

// Lease is a sandbox lease the hub granted.
type Lease struct {
	ID, Slug, Token, Profile, Purpose string
	Labels                            map[string]string
	ExpiresAt                         time.Time
	// Seq is where the grant is in the order of the requests and ReleasedSeq where the
	// release is, 0 while the lease is held.
	Seq, ReleasedSeq int
	Released         bool
}

// Leases are the leases the hub granted, in order.
func (h *Hub) Leases() []Lease {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Lease, len(h.leases))
	for i, l := range h.leases {
		out[i] = *l
	}
	return out
}

// SetLeaseTTL sets how long the leases that are granted from now on last: 30 minutes at first.
func (h *Hub) SetLeaseTTL(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.leaseTTL = d
}

// Gateway is the gateway the leases point at: agentkit's gatewayd, served on a loopback port,
// over the local provider. Its backend records every spec it is asked to create and every box
// it closes.
type Gateway struct {
	h   *Hub
	url string
	srv *gatewayd.Server

	mu       sync.Mutex
	created  []Created
	closed   []Closed
	executed []Executed
	killOn   string
}

// Created is a box the gateway created.
type Created struct {
	Seq int
	// ID is the id of the box, Lease the lease whose token asked for it.
	ID, Lease string
	// Spec is the spec the box was asked for.
	Spec sandbox.Spec
}

// Closed is a box the gateway closed.
type Closed struct {
	Seq int
	ID  string
}

// Gateway is the gateway of the hub, which is started when it is first asked for, or when the first
// lease is granted, and stopped when the test ends.
func (h *Hub) Gateway() *Gateway {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gateway == nil {
		h.gateway = h.startGateway()
	}
	return h.gateway
}

func (h *Hub) startGateway() *Gateway {
	g := &Gateway{h: h}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		h.t.Fatalf("fakehub: the gateway cannot listen: %v", err)
	}
	srv, err := gatewayd.New(gatewayd.Config{
		Backend:      &backend{Provider: local.Unisolated(), g: g},
		Authenticate: g.authenticate,
		NetworkModes: []sandbox.NetworkMode{sandbox.NetworkNone, sandbox.NetworkUnrestricted},
	})
	if err != nil {
		h.t.Fatalf("fakehub: the gateway cannot be made: %v", err)
	}
	g.srv, g.url = srv, "http://"+ln.Addr().String()
	go func() { _ = srv.Serve(ln) }()
	h.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return g
}

// URL is the URL of the gateway.
func (g *Gateway) URL() string { return g.url }

// Created are the boxes the gateway created, in order.
func (g *Gateway) Created() []Created {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.created)
}

// Closed are the boxes the gateway closed, in order.
func (g *Gateway) Closed() []Closed {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.closed)
}

// authenticate accepts the token of a lease that is held and has time left, and names the lease
// as the owner of the boxes it creates.
func (g *Gateway) authenticate(_ context.Context, token string) (string, bool) {
	g.h.mu.Lock()
	defer g.h.mu.Unlock()
	for _, l := range g.h.leases {
		if l.Token == token && !l.Released && time.Now().Before(l.ExpiresAt) {
			return l.ID, true
		}
	}
	return "", false
}

// backend is the provider behind the gateway: the local provider, with a record.
type backend struct {
	sandbox.Provider
	g *Gateway
}

func (b *backend) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	lease, _ := gatewayd.PrincipalFromContext(ctx)
	sb, err := b.Provider.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	seq := b.g.h.next()
	copied := spec
	copied.Env = copyMap(spec.Env)
	copied.Labels = copyMap(spec.Labels)
	b.g.mu.Lock()
	b.g.created = append(b.g.created, Created{Seq: seq, ID: sb.ID(), Lease: lease, Spec: copied})
	b.g.mu.Unlock()
	return &recorded{Sandbox: sb, g: b.g}, nil
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// recorded is a box of the backend whose closing is noted. The gateway reaches the agent of the
// box by following Unwrap.
type recorded struct {
	sandbox.Sandbox
	g *Gateway
}

func (r *recorded) Unwrap() sandbox.Sandbox { return r.Sandbox }

func (r *recorded) Close(ctx context.Context) error {
	err := r.Sandbox.Close(ctx)
	seq := r.g.h.next()
	r.g.mu.Lock()
	r.g.closed = append(r.g.closed, Closed{Seq: seq, ID: r.Sandbox.ID()})
	r.g.mu.Unlock()
	return err
}

// sandboxRoute serves the routes under …/sandboxes: the lease, and its release.
func (h *Hub) sandboxRoute(w http.ResponseWriter, r *http.Request, ws *workspace, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodPost:
		h.grantLease(w, r, ws)
	case len(rest) == 1 && r.Method == http.MethodDelete:
		h.releaseLease(w, ws, rest[0])
	default:
		writeError(w, http.StatusNotFound, "not_found", "not found")
	}
}

// grantLease answers the lease request: {profile?, purpose, labels}, and no other field.
func (h *Hub) grantLease(w http.ResponseWriter, r *http.Request, ws *workspace) {
	var req struct {
		Profile string            `json:"profile"`
		Purpose string            `json:"purpose"`
		Labels  map[string]string `json:"labels"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is malformed")
		return
	}
	gw := h.Gateway()
	var id, tok [16]byte
	var secret [33]byte
	_, _ = rand.Read(id[:])
	_, _ = rand.Read(secret[:])
	_ = tok
	h.mu.Lock()
	h.seq++
	l := &Lease{
		ID: "sbx_" + hex.EncodeToString(id[:]), Slug: ws.Slug, Token: "sbl_" + base64.RawURLEncoding.EncodeToString(secret[:]),
		Profile: req.Profile, Purpose: req.Purpose, Labels: req.Labels, ExpiresAt: time.Now().Add(h.leaseTTL), Seq: h.seq,
	}
	h.leases = append(h.leases, l)
	h.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": l.ID, "workspace": l.Slug, "profile": l.Profile, "purpose": l.Purpose, "state": "leased", "gateway_url": gw.URL(),
		"expires_at": l.ExpiresAt.UTC().Format(time.RFC3339), "labels": l.Labels, "token": l.Token,
	})
}

// releaseLease answers DELETE …/sandboxes/{id}: the lease ends, and the boxes it still holds
// are destroyed.
func (h *Hub) releaseLease(w http.ResponseWriter, ws *workspace, id string) {
	h.mu.Lock()
	var found *Lease
	for _, l := range h.leases {
		if l.ID == id && l.Slug == ws.Slug {
			found = l
		}
	}
	if found == nil || !strings.HasPrefix(id, "sbx_") {
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, "not_found", "lease not found")
		return
	}
	h.seq++
	found.Released, found.ReleasedSeq = true, h.seq
	gw := h.gateway
	h.mu.Unlock()

	if gw != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, c := range gw.Created() {
			if c.Lease == id && !slices.ContainsFunc(gw.Closed(), func(x Closed) bool { return x.ID == c.ID }) {
				_ = gw.srv.Destroy(ctx, c.ID)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "state": "destroyed"})
}

// Executed is a command a client ran in a box of the gateway, as the execute tool of the box was
// asked to run it.
type Executed struct {
	Seq int
	// Box is the id of the box, and Command the command line.
	Box, Command string
}

// Commands are the commands run in the boxes of the gateway through a provider that
// Instrument wrapped, in order.
func (g *Gateway) Commands() []Executed {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.executed)
}

// KillOn makes the gateway go away, as a lost connection does, when a command that holds substr
// is about to run: the command is not run, and neither is any call that follows it.
func (g *Gateway) KillOn(substr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.killOn = substr
}

// Instrument wraps a provider that dials the gateway (the client side of the wire), so that the
// commands its boxes run are recorded in the order of the gateway's own events. The gateway
// relays the protocol of the agent to the box without looking into it, so a command is seen
// where the box's execute tool is called.
func (g *Gateway) Instrument(p sandbox.Provider) sandbox.Provider {
	return &instrumented{Provider: p, g: g}
}

type instrumented struct {
	sandbox.Provider
	g *Gateway
}

func (i *instrumented) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	sb, err := i.Provider.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &watched{Sandbox: sb, g: i.g}, nil
}

// Unwrap lets a caller that unwraps providers reach the one that was wrapped.
func (i *instrumented) Unwrap() sandbox.Provider { return i.Provider }

// Close closes the provider that was wrapped, if it holds anything.
func (i *instrumented) Close() error {
	if c, ok := i.Provider.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

type watched struct {
	sandbox.Sandbox
	g *Gateway
}

func (w *watched) Unwrap() sandbox.Sandbox { return w.Sandbox }

func (w *watched) Tools(ctx context.Context, opts sandbox.ToolOptions) ([]core.Tool, error) {
	tools, err := w.Sandbox.Tools(ctx, opts)
	if err != nil {
		return nil, err
	}
	out := make([]core.Tool, len(tools))
	for i, t := range tools {
		out[i] = t
		if t.Name != "execute" {
			continue
		}
		run := t.Execute
		out[i].Execute = func(ctx context.Context, raw json.RawMessage) core.ToolResult {
			var in struct {
				Command string `json:"command"`
			}
			_ = json.Unmarshal(raw, &in)
			w.g.noteCommand(w.Sandbox.ID(), in.Command)
			return run(ctx, raw)
		}
	}
	return out, nil
}

// noteCommand records a command, and kills the gateway first if the command is the one that was
// to end it.
func (g *Gateway) noteCommand(box, command string) {
	seq := g.h.next()
	g.mu.Lock()
	g.executed = append(g.executed, Executed{Seq: seq, Box: box, Command: command})
	kill := g.killOn != "" && strings.Contains(command, g.killOn)
	g.mu.Unlock()
	if kill {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = g.srv.Shutdown(ctx)
	}
}
