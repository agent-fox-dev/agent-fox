package store

// migrations is the ordered, append-only list of the schema's changes. The
// position of a migration in the list, counted from 1, is its version, so a
// migration is never edited or removed once a release has it: a change is a
// migration at the end.
//
// Every table is named with the prefix af_, so that the tables of other modules
// that use the same file (aflib/jobqueue's jobs, for one) cannot collide with it.
var migrations = []string{
	// 1: the subscriptions. A subscription is a hub workspace, identified by its
	// slug, with the token that the service acts on it with, sealed, and the hub
	// user behind that token when hub said which it was (NULL when it did not).
	// The times are RFC 3339 in UTC. The revision is 1 for a new row and goes up
	// with each replacement, which is how the one statement that stores a
	// subscription tells a created row from a replaced one.
	`CREATE TABLE af_subscriptions (
		slug          TEXT    NOT NULL PRIMARY KEY,
		label_prefix  TEXT    NOT NULL,
		sealed_token  BLOB    NOT NULL,
		token_user    TEXT,
		subscribed_by TEXT    NOT NULL,
		created_at    TEXT    NOT NULL,
		updated_at    TEXT    NOT NULL,
		revision      INTEGER NOT NULL DEFAULT 1
	)`,
}
