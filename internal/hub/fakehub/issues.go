package fakehub

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// labelKey is a label of an issue.
type labelKey struct {
	issue int
	label string
}

// listState is what the issues list of a workspace remembers: the lags with which a forge shows what was
// changed, the order it answers in, and whether it says that a list has a further page.
type listState struct {
	// removalLag and additionLag are the number of lists with which the labels removed and added from now on lag.
	removalLag, additionLag int
	// ghosts are the labels that were removed and that their issues are still listed under, for the number of
	// lists that is left; late are the labels that were added and that their issues are still left out of the lists of.
	ghosts, late map[labelKey]int
	// order are the issues that come first, in this order, and no links makes the hub say nothing of a further page.
	order   []int
	noLinks bool
}

// removed notes that the label was taken off the issue.
func (l *listState) removed(issue int, label string) {
	k := labelKey{issue, label}
	delete(l.late, k)
	if l.removalLag > 0 {
		if l.ghosts == nil {
			l.ghosts = map[labelKey]int{}
		}
		l.ghosts[k] = l.removalLag
	}
}

// added notes that the label was put on the issue.
func (l *listState) added(issue int, label string) {
	k := labelKey{issue, label}
	delete(l.ghosts, k)
	if l.additionLag > 0 {
		if l.late == nil {
			l.late = map[labelKey]int{}
		}
		l.late[k] = l.additionLag
	}
}

// listed says whether a list of the label shows the issue.
func (l *listState) listed(i *issueState, label string) bool {
	k := labelKey{i.Number, label}
	return slices.Contains(i.Labels, label) && l.late[k] == 0 || l.ghosts[k] > 0
}

// counted ends one list of each of the labels for the lags that wait for it.
func (l *listState) counted(labels []string) {
	for _, m := range []map[labelKey]int{l.ghosts, l.late} {
		for k, left := range m {
			if slices.Contains(labels, k.label) {
				if left <= 1 {
					delete(m, k)
				} else {
					m[k] = left - 1
				}
			}
		}
	}
}

// relabel gives the issue the labels, and notes the lags of those that went and those that came.
func (ws *workspace) relabel(i *issueState, next []string) {
	for _, l := range i.Labels {
		if !slices.Contains(next, l) {
			ws.lists.removed(i.Number, l)
		}
	}
	for _, l := range next {
		if !slices.Contains(i.Labels, l) {
			ws.lists.added(i.Number, l)
		}
	}
	i.Labels = slices.Clone(next)
}

// update does change to the workspace, under the lock, and panics for a workspace the hub does not have.
func (h *Hub) update(slug string, change func(ws *workspace)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws := h.workspaces[slug]
	if ws == nil {
		panic("fakehub: no workspace " + slug)
	}
	change(ws)
}

// SetRemovalLag makes an issue stay listed under a label for the next lists of the label after the label was removed,
// for the labels that are removed from now on, as a forge whose index lags does.
func (h *Hub) SetRemovalLag(slug string, lists int) {
	h.update(slug, func(ws *workspace) { ws.lists.removalLag = lists })
}

// SetAdditionLag makes an issue be left out of the lists of a label for the next lists of the label after the label
// was added, for the labels that are added from now on.
func (h *Hub) SetAdditionLag(slug string, lists int) {
	h.update(slug, func(ws *workspace) { ws.lists.additionLag = lists })
}

// SetListOrder makes the issues of the numbers come first in a list, in this order, and the others after them by
// number.
func (h *Hub) SetListOrder(slug string, numbers ...int) {
	h.update(slug, func(ws *workspace) { ws.lists.order = slices.Clone(numbers) })
}

// SetPageLinks says whether a list that has a further page tells so with a Link header; it does, unless a test says
// that it does not, as hub's own issues list does not.
func (h *Hub) SetPageLinks(slug string, on bool) {
	h.update(slug, func(ws *workspace) { ws.lists.noLinks = !on })
}

// AddLabel adds the label to the issue, as a person would.
func (h *Hub) AddLabel(slug string, n int, label string) {
	h.update(slug, func(ws *workspace) {
		issue := ws.issues[n]
		if issue == nil {
			panic("fakehub: AddLabel: no issue " + strconv.Itoa(n))
		}
		if !slices.Contains(issue.Labels, label) {
			ws.relabel(issue, append(slices.Clone(issue.Labels), label))
		}
	})
}

// RemoveLabel removes the label from the issue, as a person would.
func (h *Hub) RemoveLabel(slug string, n int, label string) {
	h.update(slug, func(ws *workspace) {
		issue := ws.issues[n]
		if issue == nil {
			panic("fakehub: RemoveLabel: no issue " + strconv.Itoa(n))
		}
		ws.relabel(issue, slices.DeleteFunc(slices.Clone(issue.Labels), func(l string) bool { return l == label }))
	})
}

// ListRequests are the requests for a list of the issues of the workspace, in order.
func (h *Hub) ListRequests(slug string) []Request {
	return h.RequestsTo(http.MethodGet, "/api/v1/workspaces/"+slug+"/issues")
}

// The sizes of a list of issues: hub's own default and largest page.
const (
	defaultListLimit = 30
	maxListLimit     = 100
)

// listIssues answers GET …/issues as hub's forge API does, with a bare array: state (open when absent), labels (all
// of them, comma-separated or repeated) and limit (30 by default, from 1 to 100) are the filter and the size of a
// page, and page, which hub's own list does not have, picks the page that a Link header named. Pull requests are in
// the list with their marker, for a client that must leave them out. The lags count the lists of the first page.
func (h *Hub) listIssues(w http.ResponseWriter, r *http.Request, ws *workspace) {
	q := r.URL.Query()
	state := q.Get("state")
	switch state {
	case "":
		state = "open"
	case "open", "closed", "all":
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "state must be open, closed or all")
		return
	}
	limit, page := defaultListLimit, 1
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxListLimit {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be a whole number from 1 to 100")
			return
		}
		limit = n
	}
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "bad_request", "page must be a positive whole number")
			return
		}
		page = n
	}
	var labels []string
	for _, v := range q["labels"] {
		for _, l := range strings.Split(v, ",") {
			if l != "" {
				labels = append(labels, l)
			}
		}
	}

	h.mu.Lock()
	numbers := make([]int, 0, len(ws.issues))
	for n := range ws.issues {
		numbers = append(numbers, n)
	}
	slices.Sort(numbers)
	numbers = slices.SortedStableFunc(slices.Values(numbers), func(a, b int) int {
		return orderOf(ws.lists.order, a) - orderOf(ws.lists.order, b)
	})
	var matching []map[string]any
	for _, n := range numbers {
		issue := ws.issues[n]
		if state != "all" && issue.State != state {
			continue
		}
		if slices.ContainsFunc(labels, func(l string) bool { return !ws.lists.listed(issue, l) }) {
			continue
		}
		// An issue that is still listed after its label was removed is listed as the index of a lagging forge has it: with the
		// label.
		shown := issue.Issue
		shown.Labels = slices.Clone(issue.Labels)
		for _, l := range labels {
			if !slices.Contains(shown.Labels, l) {
				shown.Labels = append(shown.Labels, l)
			}
		}
		matching = append(matching, issueJSON(ws.Slug, shown))
	}
	if page == 1 {
		ws.lists.counted(labels)
	}
	noLinks := ws.lists.noLinks
	h.mu.Unlock()

	start := min((page-1)*limit, len(matching))
	end := min(start+limit, len(matching))
	out := make([]map[string]any, 0, end-start)
	out = append(out, matching[start:end]...)
	if end < len(matching) && !noLinks {
		q.Set("page", strconv.Itoa(page+1))
		w.Header().Set("Link", "<"+r.URL.Path+"?"+q.Encode()+`>; rel="next"`)
	}
	writeJSON(w, http.StatusOK, out)
}

// orderOf is where the issue comes in the order that a test asked for: the position it was named at, and after all
// of those, the same for any other.
func orderOf(order []int, n int) int {
	if i := slices.Index(order, n); i >= 0 {
		return i
	}
	return len(order)
}
