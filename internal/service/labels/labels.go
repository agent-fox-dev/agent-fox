// Package labels is the one table of the agent-fox label protocol: the seven labels that a subscribed repository
// carries, with the kind, the colour and the description of each. The subscribe step creates them, the worker's mirror
// and the cancel cleanup write them, and the label poll reads the request labels; all of them take the names from here.
//
// A label's name is the subscription's label prefix followed by the suffix. The variable AF_LABEL_PREFIX names nothing:
// the prefix comes from the subscription only, so that no part of the service writes a label that the subscribe step
// did not create.
//
// The package imports only the standard library, so that every part of the service can import it.
package labels

// Kind says what a label is for.
type Kind string

// The kinds of label: a request label asks agent-fox for a job and is consumed by it; a status label mirrors the job's
// record, one at a time.
const (
	Request Kind = "request"
	Status  Kind = "status"
)

// Label is a row of the table. Color is six lowercase hex digits without '#'.
type Label struct {
	Suffix      string
	Kind        Kind
	Color       string
	Description string
}

// The suffixes of the seven labels.
const (
	Triage     = "triage"
	Fix        = "fix"
	InProgress = "in-progress"
	Ready      = "ready"
	NeedsHuman = "needs-human"
	Landed     = "landed"
	Failed     = "failed"
)

// table is the label protocol, in its order.
var table = [...]Label{
	{Suffix: Triage, Kind: Request, Color: "0e8a16", Description: "Ask agent-fox to triage this issue"},
	{Suffix: Fix, Kind: Request, Color: "0e8a16", Description: "Ask agent-fox to fix this issue"},
	{Suffix: InProgress, Kind: Status, Color: "fbca04", Description: "agent-fox is working on this issue"},
	{Suffix: Ready, Kind: Status, Color: "1d76db", Description: "Triaged: the diagnosis is in the body"},
	{Suffix: NeedsHuman, Kind: Status, Color: "d93f0b", Description: "agent-fox stopped with a question; see the last comment"},
	{Suffix: Landed, Kind: Status, Color: "0e8a16", Description: "A branch and a pull request (or a carry patch) exist"},
	{Suffix: Failed, Kind: Status, Color: "b60205", Description: "agent-fox could not finish; see the last comment"},
}

// Table is the seven labels in order: triage, fix, in-progress, ready, needs-human, landed, failed. The slice is the
// caller's own.
func Table() []Label { return append([]Label(nil), table[:]...) }

// Requests are the suffixes of the request labels, in table order.
func Requests() []string { return suffixes(Request) }

// Statuses are the suffixes of the status labels, in table order.
func Statuses() []string { return suffixes(Status) }

func suffixes(kind Kind) []string {
	var out []string
	for _, l := range table {
		if l.Kind == kind {
			out = append(out, l.Suffix)
		}
	}
	return out
}

// Name is the name of the label with the suffix under the prefix of a subscription.
func Name(prefix, suffix string) string { return prefix + suffix }

// Names are the names of the seven labels under the prefix, in table order.
func Names(prefix string) []string {
	out := make([]string, len(table))
	for i, l := range table {
		out[i] = Name(prefix, l.Suffix)
	}
	return out
}
