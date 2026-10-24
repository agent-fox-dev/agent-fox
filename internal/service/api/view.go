package api

import (
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// subscriptionView is how a subscription is shown to a caller. It holds neither
// the token nor its ciphertext, whatever the caller. token_user is null when hub did not say whose
// token it is.
type subscriptionView struct {
	Slug         string    `json:"slug"`
	TokenUser    *string   `json:"token_user"`
	SubscribedBy string    `json:"subscribed_by"`
	TokenState   string    `json:"token_state"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// viewOf is the view of a subscription.
func viewOf(sub store.Subscription) subscriptionView {
	view := subscriptionView{
		Slug:         sub.Slug,
		SubscribedBy: sub.SubscribedBy,
		TokenState:   sub.TokenState,
		CreatedAt:    sub.CreatedAt,
		UpdatedAt:    sub.UpdatedAt,
	}
	if sub.TokenUser != "" {
		view.TokenUser = &sub.TokenUser
	}
	return view
}
