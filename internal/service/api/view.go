package api

import (
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// subscriptionView is how a subscription is shown to a caller. It holds neither
// the token nor its ciphertext, whatever the caller. label_prefix is the prefix
// that was given when the workspace was subscribed, which is not necessarily the
// one in effect: whether it or hub's variable governs the labels is for the
// code that applies labels to say. token_user is null when hub did not say whose
// token it is.
type subscriptionView struct {
	Slug         string    `json:"slug"`
	LabelPrefix  string    `json:"label_prefix"`
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
		LabelPrefix:  sub.LabelPrefix,
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
