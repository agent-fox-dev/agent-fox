package labels_test

import (
	"regexp"
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/labels"
)

func TestTableHoldsTheSevenLabelsInOrderWithTheirKinds(t *testing.T) {
	table := labels.Table()
	var suffixes []string
	hex := regexp.MustCompile(`^[0-9a-f]{6}$`)
	for _, l := range table {
		suffixes = append(suffixes, l.Suffix)
		if !hex.MatchString(l.Color) {
			t.Errorf("%s: colour %q is not six lowercase hex digits without '#'", l.Suffix, l.Color)
		}
		if l.Description == "" {
			t.Errorf("%s has no description", l.Suffix)
		}
		if want := slices.Contains([]string{labels.Triage, labels.Fix}, l.Suffix); (l.Kind == labels.Request) != want {
			t.Errorf("%s is of kind %s", l.Suffix, l.Kind)
		}
	}
	want := []string{labels.Triage, labels.Fix, labels.InProgress, labels.Ready, labels.NeedsHuman, labels.Landed, labels.Failed}
	if !slices.Equal(suffixes, want) {
		t.Errorf("the table is %v, want %v", suffixes, want)
	}
	if got := labels.Requests(); !slices.Equal(got, want[:2]) {
		t.Errorf("Requests() = %v, want %v", got, want[:2])
	}
	if got := labels.Statuses(); !slices.Equal(got, want[2:]) {
		t.Errorf("Statuses() = %v, want %v", got, want[2:])
	}
}

func TestTableIsTheCallersOwnCopy(t *testing.T) {
	table := labels.Table()
	table[0].Suffix = "changed"
	if labels.Table()[0].Suffix != labels.Triage {
		t.Error("changing the slice that Table returned changed the table")
	}
}

func TestNamesArePrefixedSuffixesInTableOrder(t *testing.T) {
	if got := labels.Name(labels.NeedsHuman); got != "af:needs-human" {
		t.Errorf("Name(needs-human) = %q", got)
	}
	names := labels.Names()
	table := labels.Table()
	if len(names) != len(table) {
		t.Fatalf("Names() has %d names for %d labels", len(names), len(table))
	}
	for i, l := range table {
		if names[i] != "af:"+l.Suffix {
			t.Errorf("Names()[%d] = %q, want af:%s", i, names[i], l.Suffix)
		}
	}
}
