package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/ttl"
)

// cacheCapacity bounds each of the two caches, so that a client that sends
// tokens or slugs without end cannot make the service hold them without end.
const cacheCapacity = 10_000

// defaultAuthCacheTTL is how long hub's answers are kept when the dependencies
// do not say.
const defaultAuthCacheTTL = 60 * time.Second

// slugPattern is what a workspace slug looks like.
var slugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// errUnauthenticated is the failure of a caller whom hub does not accept.
var errUnauthenticated = errors.New("hub does not accept the token")

// identity is what hub said about a caller's token: the user it belongs to, or
// that it refuses the token.
type identity struct {
	user    string
	refused bool
}

// visibilityKey is a caller's token, by its hash, and a workspace.
type visibilityKey struct{ tokenHash, slug string }

// auth is hub's word about who is calling and which workspaces they may see, kept
// for a while so that hub is not asked on every request. Hub is the only
// authority: the service has no users of its own.
//
// Both caches are keyed by the hash of the token, never by the token. They hold
// only answers about the caller's token. A call that is made with the token of a
// subscription (the PAT of a workspace, which a caller hands over to subscribe
// it) goes to the hub client directly and never reads, fills or evicts either
// cache, even when that token is the same string as the caller's.
type auth struct {
	hub        *hub.Client
	identities *ttl.Cache[string, identity]
	visibility *ttl.Cache[visibilityKey, bool]
}

func newAuth(c *hub.Client, d time.Duration, now func() time.Time) *auth {
	if d <= 0 {
		d = defaultAuthCacheTTL
	}
	return &auth{
		hub:        c,
		identities: ttl.New[string, identity](cacheCapacity, d, now),
		visibility: ttl.New[visibilityKey, bool](cacheCapacity, d, now),
	}
}

// hashToken is the hex SHA-256 of a token: the key of the caches.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// identify is the hub user the token belongs to. A hit is used without calling
// hub; a refusal by hub is kept as well; hub being unavailable is not kept.
func (a *auth) identify(ctx context.Context, token string) (string, error) {
	hash := hashToken(token)
	if known, ok := a.identities.Get(hash); ok {
		if known.refused {
			return "", errUnauthenticated
		}
		return known.user, nil
	}
	user, err := a.hub.User(ctx, token)
	switch {
	case err == nil:
		a.identities.Put(hash, identity{user: user.ID})
		return user.ID, nil
	case errors.Is(err, hub.ErrRefused):
		a.identities.Put(hash, identity{refused: true})
		return "", errUnauthenticated
	}
	return "", err
}

// visible reports whether the caller's token sees the workspace on hub. Found
// and not found are kept; hub being unavailable is not. When hub refuses the
// token on this call, the token is no longer good, and everything kept about it
// goes: errUnauthenticated.
func (a *auth) visible(ctx context.Context, token, slug string) (bool, error) {
	hash := hashToken(token)
	key := visibilityKey{hash, slug}
	if seen, ok := a.visibility.Get(key); ok {
		return seen, nil
	}
	err := a.hub.Workspace(ctx, token, slug)
	switch {
	case err == nil:
		a.visibility.Put(key, true)
		return true, nil
	case errors.Is(err, hub.ErrNotFound):
		a.visibility.Put(key, false)
		return false, nil
	case errors.Is(err, hub.ErrRefused):
		a.identities.Delete(hash)
		a.visibility.DeleteIf(func(k visibilityKey, _ bool) bool { return k.tokenHash == hash })
		return false, errUnauthenticated
	}
	return false, err
}

// principal is the caller of a request: the hub user, and the token that showed it.
type principal struct{ user, token string }

// principalKey is the key of the principal in the context of a request.
type principalKey struct{}

// principalOf is the caller of a request that went through authed or inWorkspace.
func principalOf(r *http.Request) principal {
	p, _ := r.Context().Value(principalKey{}).(principal)
	return p
}

// bearerToken is the token of an Authorization: Bearer header. The scheme is
// matched without regard to case, and the token must not be empty.
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// authed wraps a handler of a route under /api/v1 in the check of who is
// calling: the Bearer token, put to hub or to the cache. The handler gets the
// caller with principalOf.
func (s *server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	}
}

// inWorkspace wraps a handler of a route with a {slug} in the checks of the API,
// in this order: the syntax of the slug (400), who is calling (401), and whether
// they see the workspace on hub (404, the same answer as for a workspace that
// does not exist). The handler gets the slug.
func (s *server) inWorkspace(h func(w http.ResponseWriter, r *http.Request, slug string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if !slugPattern.MatchString(slug) {
			writeError(w, CodeInvalidRequest, "the workspace slug is not valid")
			return
		}
		p, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		seen, err := s.auth.visible(r.Context(), p.token, slug)
		if err != nil {
			s.hubFailure(w, r, err)
			return
		}
		if !seen {
			writeNotFound(w)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)), slug)
	}
}

// authenticate settles who is calling, or answers the request and reports false.
func (s *server) authenticate(w http.ResponseWriter, r *http.Request) (principal, bool) {
	token, ok := bearerToken(r)
	if !ok {
		unauthenticated(w, "a Bearer token is required")
		return principal{}, false
	}
	user, err := s.auth.identify(r.Context(), token)
	if err != nil {
		s.hubFailure(w, r, err)
		return principal{}, false
	}
	setCaller(r, user)
	return principal{user: user, token: token}, true
}

// hubFailure answers a failed call to hub: 401 for a caller's token that hub does
// not accept, 502 when hub could not say.
func (s *server) hubFailure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errUnauthenticated) {
		unauthenticated(w, "hub does not accept the token")
		return
	}
	if _, unavailable := errors.AsType[*hub.UnavailableError](err); unavailable {
		// The error holds neither the token nor the URL of the call.
		s.log.LogAttrs(r.Context(), slog.LevelWarn, "hub is unavailable", slog.String("error", err.Error()))
		writeError(w, CodeHubUnavailable, "hub is unavailable")
		return
	}
	s.log.LogAttrs(r.Context(), slog.LevelError, "an unexpected failure of the check of the caller", slog.String("error", err.Error()))
	writeError(w, CodeInternal, "internal error")
}

// unauthenticated answers 401.
func unauthenticated(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, CodeUnauthenticated, message)
}

// writeNotFound answers 404 for a workspace. One answer serves a workspace that
// does not exist, one that is not subscribed and one that the caller may not
// see, with nothing of the slug in it, so that nobody learns from the answer
// which of the three it was.
func writeNotFound(w http.ResponseWriter) {
	writeError(w, CodeNotFound, "workspace not found")
}
