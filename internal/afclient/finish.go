package afclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// FinishedAttempt is the number of the highest-numbered attempt of the job whose state is not
// queued or running, from the job document (the single-job view, which has the attempts), or 0 when
// no attempt has finished.
func FinishedAttempt(job json.RawMessage) int {
	var doc struct {
		Attempts []struct {
			N     int    `json:"n"`
			State string `json:"state"`
		} `json:"attempts"`
	}
	if json.Unmarshal(job, &doc) != nil {
		return 0
	}
	finished := 0
	for _, a := range doc.Attempts {
		if a.State != "queued" && a.State != "running" && a.N > finished {
			finished = a.N
		}
	}
	return finished
}

// Envelope fetches GET /api/v1/jobs/{id}/attempts/{n}/envelope for the highest-numbered finished
// attempt n of the job, whose document is job. It returns the envelope as the service stored it.
// There is none, and the envelope is nil, when no attempt has finished or the service answers 404.
// Any other failure, an envelope that is not JSON included, leaves it nil too and gives the
// reason in unavailable, which a command raises as a warning. Only a call that ctx stopped is an
// error.
func (c *Client) Envelope(ctx context.Context, id string, job json.RawMessage) (envelope json.RawMessage, unavailable string, err error) {
	n := FinishedAttempt(job)
	if n == 0 {
		return nil, "", nil
	}
	resp, rerr := c.Do(ctx, http.MethodGet, "/jobs/"+url.PathEscape(id)+"/attempts/"+strconv.Itoa(n)+"/envelope", nil, nil)
	if rerr != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		if ae, ok := rerr.(*Error); ok && ae.Status == http.StatusNotFound {
			return nil, "", nil
		}
		return nil, fmt.Sprintf("the envelope of attempt %d of the job %s could not be fetched: %v", n, id, rerr), nil
	}
	if !json.Valid(resp.Body) {
		return nil, fmt.Sprintf("the envelope of attempt %d of the job %s is not JSON", n, id), nil
	}
	return resp.Body, "", nil
}
