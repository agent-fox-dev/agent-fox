package worker

import (
	"fmt"
	"net/url"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// refusal is why an attempt stops before its loop runs, with the category, the message and the
// hint of the envelope that says so. It is never retryable: what can be tried again is an
// infrastructure failure, which is a transient error.
type refusal struct {
	category string
	message  string
	hint     *toolio.FixHint
}

// usageRefusal is a refusal of the request.
func usageRefusal(format string, args ...any) *refusal {
	return &refusal{category: toolio.CategoryUsage, message: fmt.Sprintf(format, args...)}
}

// replaceToken is where a token of a subscription is replaced.
func replaceToken(slug string) string { return "PUT /api/v1/workspaces/" + url.PathEscape(slug) }

// authRefusal is the refusal of a token that hub does not take, a token that cannot be read, and a
// subscription that is gone: all of them are the credential, and the way out is the same.
func authRefusal(slug, why string) *refusal {
	return &refusal{category: toolio.CategoryAuth, message: fmt.Sprintf("%s: replace the token of the subscription with %s", why, replaceToken(slug))}
}

// envelopeOf makes the result of an attempt that ends with a refusal: an envelope of the tool
// that has nothing in it but the error, encoded as the shell encodes envelopes. The attempt has
// run nothing, so it has no usage, no artifact and no side effect.
func (r *Runner) envelopeOf(a *attempt, f *refusal) *outcome {
	env := toolio.NewEnvelope(string(a.req.Kind))
	env.Fail(&toolio.Failure{Category: f.category, Message: f.message, FixHint: f.hint})
	env.Seal(a.started, r.cfg.Now())
	data, err := env.Marshal()
	if err != nil {
		return &outcome{err: fmt.Errorf("worker: the envelope of a refusal cannot be encoded: %w", err)}
	}
	return &outcome{result: jobs.Result{Envelope: data, MaxAttempts: a.maxAttempts}}
}

// transientFailure is the outcome of an infrastructure failure: a transient error that carries
// the failure, and no envelope, so that the jobs layer hands the same attempt over again.
func transientFailure(category, message string) *outcome {
	return &outcome{err: jobs.Transient(&toolio.Failure{Category: category, Message: message, Retryable: true})}
}
