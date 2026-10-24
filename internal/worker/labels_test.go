package worker

import (
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/labels"
)

// TestTheWorkerWritesTheSameLabelsWithNamesFromTheTable_TS_17_4 verifies TS-17-4: the worker takes its status labels from
// the label table and writes, for every transition and for a running cancel, the labels and the warnings it wrote before.
// The expectations are the worker's own mirror tests, which run here unchanged.
// Verifies: 17-REQ-1.4
func TestTheWorkerWritesTheSameLabelsWithNamesFromTheTable_TS_17_4(t *testing.T) {
	if !slices.Equal(statuses, labels.Statuses()) {
		t.Fatalf("the worker's status labels are %v, want the table's %v", statuses, labels.Statuses())
	}
	for _, s := range []string{statusInProgress, statusReady, statusNeedsHuman, statusLanded, statusFailed} {
		if !slices.Contains(labels.Statuses(), s) {
			t.Errorf("the worker's status %q is not a status label of the table", s)
		}
	}
	for name, test := range map[string]func(*testing.T){
		"one status at a time, ready kept by a fix":   TestSettingAStatusLabelRemovesTheOtherFourExceptThatAFixsInProgressKeepsReady_TS_13_67,
		"each outcome's label and comment":            TestEachOutcomeMapsToItsStatusLabelAndComment_TS_13_68,
		"a transient error removes only in-progress":  TestATransientErrorOrAFailureAnAutomaticAttemptFollowsOnlyRemovesInProgress_TS_13_69,
		"failed writes are warnings":                  TestAFailedForgeWriteIsAWarningAndASideEffectAndNeverAStatusChange_TS_13_71,
		"a running cancel restores the request label": TestACancelledRunningAttemptRestoresTheRequestLabelWithinTheGrace_TS_13_72,
	} {
		t.Run(name, test)
	}
}
