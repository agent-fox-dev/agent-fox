package store

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

// firstMigrations are the SHA-256 of the migrations that the service core and the jobs layer appended, which a release
// has: a migration is never edited, so the poll's is appended after them.
var firstMigrations = []string{
	"9b51a8ec8a38207cc0c1ae18a1451ac5d118e011e07a20401db54117952e2dd1", // the subscriptions
	"f9587ed6ca9f418668f7e0523b7d271c205c7d5fa4dbdda0f6e8bc019e040b60", // the jobs
	"f2ac97668cb31c8b649fada2ee58f2201fc8de8faaed894d2818457df9666795", // the attempts
	"6abaff30d53e4c5280b99c5d6258bb87c4791c6239d0bd8b941210a0326fc9f4", // the events
}

// TestTheMigrationOfThePollIsAppendedAndTheEarlierOnesAreAsTheyWere_TS_15_37 verifies TS-15-37, the part that only the
// package can show: the first four migrations are byte for byte what they were, and the fifth creates af_poll_requests.
// The table, its lack of a foreign key and the survival of its rows are shown by the tests of internal/service/poll.
// Verifies: 15-REQ-6.1
func TestTheMigrationOfThePollIsAppendedAndTheEarlierOnesAreAsTheyWere_TS_15_37(t *testing.T) {
	if len(migrations) < len(firstMigrations)+1 {
		t.Fatalf("%d migrations, want the four that were there and the poll's after them", len(migrations))
	}
	for i, want := range firstMigrations {
		if got := fmt.Sprintf("%x", sha256.Sum256([]byte(migrations[i]))); got != want {
			t.Errorf("migration %d is %s, want it unchanged: %s", i+1, got, want)
		}
	}
	poll := migrations[len(firstMigrations)]
	if !strings.Contains(poll, "CREATE TABLE af_poll_requests") {
		t.Errorf("migration %d is %q, want the one that creates af_poll_requests", len(firstMigrations)+1, poll)
	}
	if strings.Contains(strings.ToUpper(poll), "REFERENCES") || strings.Contains(strings.ToUpper(poll), "FOREIGN KEY") {
		t.Errorf("migration %d has a foreign key: %q", len(firstMigrations)+1, poll)
	}
}
