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

	// 2: the jobs. A job is the durable record of one triage or fix request against
	// a workspace; the input is the JSON that was posted without the context, which
	// is kept apart. The partial unique index keeps a second open job of the same
	// kind for the same issue out of a workspace, however the two requests race.
	// The times are RFC 3339 in UTC with milliseconds. A job has a finished_at
	// exactly when it is done, failed or cancelled.
	`CREATE TABLE af_jobs (
		id             TEXT NOT NULL PRIMARY KEY,
		workspace      TEXT NOT NULL,
		kind           TEXT NOT NULL CHECK (kind IN ('triage', 'fix')),
		input          TEXT NOT NULL,
		context        TEXT NOT NULL,
		issue          INTEGER,
		source_kind    TEXT NOT NULL,
		source_by      TEXT NOT NULL,
		request_label  TEXT,
		state          TEXT NOT NULL CHECK (state IN ('queued', 'running', 'blocked', 'done', 'failed', 'cancelled')),
		labels_set     TEXT,
		labels_removed TEXT,
		labels_restore TEXT,
		created_at     TEXT NOT NULL,
		started_at     TEXT,
		finished_at    TEXT
	);
	CREATE INDEX af_jobs_by_created ON af_jobs (created_at, id);
	CREATE INDEX af_jobs_by_workspace ON af_jobs (workspace, created_at, id);
	CREATE UNIQUE INDEX af_jobs_one_open_per_issue ON af_jobs (workspace, kind, issue)
		WHERE issue IS NOT NULL AND state IN ('queued', 'running', 'blocked')`,

	// 3: the attempts, keyed by the job and the number of the attempt. queue_seq
	// numbers the attempts of all jobs in the order they were made, which is the
	// order a workspace runs them in. question and options are what the attempt
	// asked, when it ended needs_human; answer is what it was started with, when a
	// person answered. status, exit_code, summary, error, artifacts, side_effects
	// and usage are those of the attempt's envelope, and are null until it has one.
	`CREATE TABLE af_attempts (
		job_id             TEXT    NOT NULL REFERENCES af_jobs (id),
		n                  INTEGER NOT NULL,
		state              TEXT    NOT NULL CHECK (state IN ('queued', 'running', 'done', 'needs_human', 'failed', 'cancelled')),
		queue_seq          INTEGER NOT NULL,
		trigger_kind       TEXT    NOT NULL CHECK (trigger_kind IN ('create', 'answer', 'retry', 'auto')),
		trigger_by         TEXT    NOT NULL,
		answer             TEXT,
		deliveries         INTEGER NOT NULL DEFAULT 0,
		transient_failures INTEGER NOT NULL DEFAULT 0,
		max_attempts       INTEGER NOT NULL DEFAULT 0,
		hub_session        TEXT,
		hub_run            TEXT,
		hub_sandbox        TEXT,
		question           TEXT,
		options            TEXT,
		status             TEXT,
		exit_code          INTEGER,
		summary            TEXT,
		error              TEXT,
		artifacts          TEXT,
		side_effects       TEXT,
		usage              TEXT,
		has_envelope       INTEGER NOT NULL DEFAULT 0,
		has_report         INTEGER NOT NULL DEFAULT 0,
		started_at         TEXT,
		finished_at        TEXT,
		PRIMARY KEY (job_id, n)
	);
	CREATE UNIQUE INDEX af_attempts_by_seq ON af_attempts (queue_seq);
	CREATE INDEX af_attempts_by_state ON af_attempts (state)`,

	// 4: the events of a job, numbered from 1 without a gap across its attempts.
	`CREATE TABLE af_events (
		job_id  TEXT    NOT NULL REFERENCES af_jobs (id),
		seq     INTEGER NOT NULL,
		attempt INTEGER NOT NULL,
		ts      TEXT    NOT NULL,
		type    TEXT    NOT NULL,
		data    TEXT    NOT NULL,
		PRIMARY KEY (job_id, seq)
	) WITHOUT ROWID`,

	// 5: the records of the label poll. A record is a label that the poll took or absorbed for an issue of a
	// workspace: whether its removal from the issue is still owed, the job the poll made for it (none for a label
	// that was absorbed), what the poll has seen of that job (open, ended, or holding the label that a cancel put
	// back) and, for a hold, when its release clock started. There is no foreign key to af_jobs on purpose: a hold
	// or an owed removal concerns a label on the forge, which outlives the job, so the retention purge of the job
	// must neither fail on the record nor take it along.
	`CREATE TABLE af_poll_requests (
		id            INTEGER PRIMARY KEY,
		workspace     TEXT    NOT NULL,
		issue         INTEGER NOT NULL,
		label         TEXT    NOT NULL,
		job_id        TEXT,
		owed          INTEGER NOT NULL DEFAULT 0 CHECK (owed IN (0, 1)),
		seen          TEXT    NOT NULL DEFAULT 'open' CHECK (seen IN ('open', 'ended', 'holding')),
		release_clock TEXT,
		created_at    TEXT    NOT NULL
	);
	CREATE INDEX af_poll_requests_by_issue ON af_poll_requests (workspace, issue, label)`,

	// 6: whether the runner of an attempt was called. A claim sets an attempt running a moment before its runner is called, and a
	// job that is cancelled in that moment never reaches a runner, so nothing puts its request label back: the label poll must
	// tell it from a job that was cancelled while its runner held the attempt. A claim clears the mark, and the call of the runner
	// sets it.
	`ALTER TABLE af_attempts ADD COLUMN in_runner INTEGER NOT NULL DEFAULT 0`,
}
