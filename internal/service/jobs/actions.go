package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxAnswerBytes is the longest answer in free text.
const maxAnswerBytes = 8 << 10

// AnswerRequest is what a person answers a question with: exactly one of a text of their
// own and the id of one of the options.
type AnswerRequest struct {
	Answer *string
	Option *string
}

// resolveAnswer checks the answer against the question and makes it what is stored: the
// question, its options, the option chosen (nil for free text), and the text, which is
// the label of the option or the text typed, trimmed.
func resolveAnswer(blocked *Blocked, req AnswerRequest) (Answer, error) {
	if (req.Answer == nil) == (req.Option == nil) {
		return Answer{}, invalid("answer", "exactly one of answer and option is required")
	}
	answer := Answer{Question: blocked.Question, Options: blocked.Options}
	if req.Answer != nil {
		text := *req.Answer
		switch {
		case !utf8.ValidString(text):
			return Answer{}, invalid("answer", "must be valid UTF-8")
		case strings.ContainsRune(text, 0):
			return Answer{}, invalid("answer", "must not contain a NUL byte")
		case strings.TrimSpace(text) == "":
			return Answer{}, invalid("answer", "must not be empty")
		case len(text) > maxAnswerBytes:
			return Answer{}, invalid("answer", "must be at most %d bytes, and is %d", maxAnswerBytes, len(text))
		}
		answer.Text = strings.TrimSpace(text)
		return answer, nil
	}
	ids := make([]string, len(blocked.Options))
	for i, o := range blocked.Options {
		ids[i] = o.ID
		if o.ID == *req.Option {
			chosen := o.ID
			answer.Option, answer.Text = &chosen, o.Label
			return answer, nil
		}
	}
	if len(ids) == 0 {
		return Answer{}, invalid("option", "the question has no options: answer in text")
	}
	return Answer{}, invalid("option", "is not one of %s", strings.Join(ids, ", "))
}

// Answer answers the question of a blocked job: attempt n+1 is queued with the trigger
// answer, by the person, and the answer, and the job is queued. The checks are, in order:
// the job is blocked (a *StateError, naming its state, if not); its workspace is subscribed
// and not being unsubscribed (ErrNotSubscribed) and its token opens (ErrTokenUnreadable);
// the answer is exactly one valid answer or option (a *ValidationError). Nothing is
// changed by a refusal. The change of state is conditional on the job still being blocked,
// so that of two answers or an answer and a cancel that race, one is a *StateError.
func (s *Service) Answer(ctx context.Context, id, by string, req AnswerRequest) (Doc, error) {
	doc, err := s.store.Doc(ctx, id, false)
	if err != nil {
		return Doc{}, err
	}
	if doc.State != StateBlocked || doc.Blocked == nil {
		return Doc{}, &StateError{State: doc.State}
	}
	if err := s.store.fails(OpObserved); err != nil {
		return Doc{}, err
	}
	return s.requeue(ctx, doc, StateBlocked, Trigger{Kind: TriggerAnswer, By: by}, func() (*Answer, error) {
		answer, err := resolveAnswer(doc.Blocked, req)
		return &answer, err
	}, nil)
}

// Retry asks again for a failed job: attempt n+1 is queued with the trigger retry, and the
// job is queued. The checks are those of Answer, without the answer; and a job whose issue
// has another open job of the kind by now is a *DuplicateError.
func (s *Service) Retry(ctx context.Context, id, by string) (Doc, error) {
	return s.RetryWithContext(ctx, id, by, nil)
}

// RetryWithContext is Retry from the forge: the new attempt carries the context, the comments a person wrote since the
// failure, which its runner is given after the job's own context. The API's retry carries none.
func (s *Service) RetryWithContext(ctx context.Context, id, by string, notes []string) (Doc, error) {
	doc, err := s.store.Doc(ctx, id, false)
	if err != nil {
		return Doc{}, err
	}
	if doc.State != StateFailed {
		return Doc{}, &StateError{State: doc.State}
	}
	if err := s.store.fails(OpObserved); err != nil {
		return Doc{}, err
	}
	return s.requeue(ctx, doc, StateFailed, Trigger{Kind: TriggerRetry, By: by}, func() (*Answer, error) { return nil, nil }, notes)
}

// requeue is what Answer and Retry share, after the state is known to be right: the
// subscription, the answer, the conditional change, and the queueing of the attempt once it
// is stored. The job is one that exists already, so an unsubscribe that starts meanwhile
// lists it, open, and cancels it: no gate is needed, as it is for a job that is made.
func (s *Service) requeue(ctx context.Context, doc Doc, from State, trigger Trigger, answer func() (*Answer, error), notes []string) (Doc, error) {
	if err := s.subscribed(ctx, doc.Workspace); err != nil {
		return Doc{}, err
	}
	resolved, err := answer()
	if err != nil {
		return Doc{}, err
	}
	n, err := s.store.RequeueWith(ctx, doc.ID, from, trigger, resolved, notes)
	if errors.Is(err, ErrDuplicate) && doc.Issue != nil {
		open, lookup := s.store.OpenJobID(ctx, doc.Workspace, doc.Kind, *doc.Issue)
		if lookup != nil {
			return Doc{}, fmt.Errorf("job %s: %w", doc.ID, lookup)
		}
		return Doc{}, &DuplicateError{JobID: open}
	}
	if err != nil {
		return Doc{}, err
	}
	queued, err := s.store.Doc(ctx, doc.ID, false)
	if err != nil {
		return Doc{}, err
	}
	if err := s.enqueue(context.WithoutCancel(ctx), doc.ID, n); err != nil {
		s.log.Warn("the attempt is stored but not in the queue; the sweep will queue it",
			"job", doc.ID, "attempt", n, "error", err.Error())
	}
	return queued, nil
}
