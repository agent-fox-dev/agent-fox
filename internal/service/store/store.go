// Package store keeps the state of the agent-fox service in one SQLite file.
//
// The file is opened in WAL mode with a busy timeout and foreign keys on, and is
// created, with its directory, as private to the user. Every table the store
// owns is named with the prefix af_, its migrations are recorded in a table of
// its own (never in PRAGMA user_version), and it neither reads nor writes any
// other table, so that the file can also hold the tables of other modules.
//
// A workspace token is kept sealed with the deployment key (see package
// secrets). A token that does not open with the key is reported as unreadable,
// and does not stop the store from opening: the subscription can still be
// replaced or removed.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/secrets"

	_ "modernc.org/sqlite" // registers the database/sql driver "sqlite"
)

// The two states of a stored token.
const (
	TokenOK         = "ok"
	TokenUnreadable = "unreadable"
)

var (
	// ErrNotSubscribed is the error for a slug that has no subscription.
	ErrNotSubscribed = errors.New("workspace is not subscribed")
	// ErrUnreadable is the error for a stored token that does not open with the key.
	ErrUnreadable = errors.New("the stored token does not open with this key")
)

// Subscription is a subscribed workspace. It holds neither the token nor its
// ciphertext.
type Subscription struct {
	Slug string
	// TokenUser is the hub user behind the token, empty when hub did not say.
	TokenUser string
	// SubscribedBy is the hub user who last subscribed the workspace.
	SubscribedBy string
	// TokenState is TokenOK or TokenUnreadable.
	TokenState string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Store is the state file.
type Store struct {
	db  *sql.DB
	key secrets.Key
}

// Open opens the state file at path, which it creates, with its parent
// directory (mode 0700) and itself (mode 0600), when it is missing, and applies
// the migrations the file has not recorded. Every error names the file.
func Open(ctx context.Context, path string, key secrets.Key) (*Store, error) {
	if err := create(path); err != nil {
		return nil, fmt.Errorf("database %s: %s", path, reason(err))
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("database %s: %s", path, reason(err))
	}
	if err := prepare(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database %s: %s", path, reason(err))
	}
	return &Store{db: db, key: key}, nil
}

// create makes the directory and the file private to the user when they are
// missing, so that the driver, which would make the file with the mode of the
// umask, finds it there. The WAL and the shared memory file of SQLite take the
// mode of the file.
func create(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// dsn is the name the driver opens the file at, with the pragmas that every
// connection of the pool is to have.
func dsn(path string) string {
	escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	return "file:" + escaped + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
}

// prepare checks that the file opens with the pragmas in force, and migrates it.
func prepare(ctx context.Context, db *sql.DB) error {
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	for pragma, want := range map[string]string{"journal_mode": "wal", "busy_timeout": "5000", "foreign_keys": "1"} {
		var got string
		if err := db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("PRAGMA %s is %q, want %q", pragma, got, want)
		}
	}
	return migrate(ctx, db, migrations, time.Now())
}

// reason is the cause of an error of the file system, without the path that
// the error repeats.
func reason(err error) string {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err.Error()
	}
	return err.Error()
}

// migrate applies the migrations the database has not recorded, in order, each
// in a transaction of its own together with its record. A database that records
// a migration beyond the list belongs to a newer binary, or to an unreleased build
// from before the migrations were squashed, and is left as it is.
func migrate(ctx context.Context, db *sql.DB, migrations []string, now time.Time) error {
	applied, err := recorded(ctx, db)
	if err != nil {
		return err
	}
	for v := range applied {
		if v > len(migrations) {
			return fmt.Errorf("the database records migration %d, but this binary knows migrations only up to %d: it was made by a newer agent-fox, or by a build from before the migrations were squashed, whose state file has to be removed", v, len(migrations))
		}
	}
	for i, statements := range migrations {
		version := i + 1
		if applied[version] {
			continue
		}
		if err := apply(ctx, db, version, statements, now); err != nil {
			return fmt.Errorf("migration %d: %w", version, err)
		}
	}
	return nil
}

// recorded is the set of migrations the database has recorded as applied.
func recorded(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	var tables int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'af_schema_migrations'").Scan(&tables); err != nil {
		return nil, err
	}
	applied := map[int]bool{}
	if tables == 0 {
		return applied, nil
	}
	rows, err := db.QueryContext(ctx, "SELECT version FROM af_schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// apply runs one migration and records it, or neither.
func apply(ctx context.Context, db *sql.DB, version int, statements string, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS af_schema_migrations (version INTEGER NOT NULL PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, statements); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO af_schema_migrations (version, applied_at) VALUES (?, ?)", version, formatTime(now)); err != nil {
		return err
	}
	return tx.Commit()
}

// formatTime is how a time is stored: RFC 3339 in UTC.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// columns are the columns of a subscription that every read selects, in the
// order scan reads them.
const columns = "slug, sealed_token, token_user, subscribed_by, created_at, updated_at, revision"

// upsert stores a subscription in one statement: a new row, or a replacement of
// the row of the slug in full, which keeps created_at and counts a revision.
const upsert = `
INSERT INTO af_subscriptions (slug, sealed_token, token_user, subscribed_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (slug) DO UPDATE SET
	sealed_token  = excluded.sealed_token,
	token_user    = excluded.token_user,
	subscribed_by = excluded.subscribed_by,
	updated_at    = excluded.updated_at,
	revision      = revision + 1
RETURNING ` + columns

// Upsert seals token for slug and stores the subscription: a new row with
// created_at and updated_at at now, or, when the slug is subscribed already, the
// row replaced in full with created_at kept. An empty tokenUser is stored as
// NULL. created says whether the row is new.
func (s *Store) Upsert(ctx context.Context, slug, token, tokenUser, subscribedBy string, now time.Time) (sub Subscription, created bool, err error) {
	sealed, err := s.key.Seal(slug, token)
	if err != nil {
		return Subscription{}, false, fmt.Errorf("subscription %s: %w", slug, err)
	}
	var user any
	if tokenUser != "" {
		user = tokenUser
	}
	stamp := formatTime(now)
	sub, revision, err := s.scan(s.db.QueryRowContext(ctx, upsert, slug, sealed, user, subscribedBy, stamp, stamp))
	if err != nil {
		return Subscription{}, false, fmt.Errorf("subscription %s: %w", slug, err)
	}
	return sub, revision == 1, nil
}

// Get is the subscription of slug, or ErrNotSubscribed.
func (s *Store) Get(ctx context.Context, slug string) (Subscription, error) {
	sub, _, err := s.scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM af_subscriptions WHERE slug = ?", slug))
	if errors.Is(err, sql.ErrNoRows) {
		return Subscription{}, fmt.Errorf("%w: %s", ErrNotSubscribed, slug)
	}
	return sub, err
}

// List is every subscription, sorted by slug.
func (s *Store) List(ctx context.Context) ([]Subscription, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM af_subscriptions ORDER BY slug")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subs := []Subscription{}
	for rows.Next() {
		sub, _, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// Delete removes the subscription of slug, its sealed token included, and says
// whether there was one.
func (s *Store) Delete(ctx context.Context, slug string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM af_subscriptions WHERE slug = ?", slug)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Token is the opened token of slug, for the code of this process that acts on
// the workspace with it. The error is ErrNotSubscribed for a slug that has no
// subscription and ErrUnreadable for a token that does not open with the key;
// neither holds the token.
func (s *Store) Token(ctx context.Context, slug string) (string, error) {
	var sealed []byte
	err := s.db.QueryRowContext(ctx, "SELECT sealed_token FROM af_subscriptions WHERE slug = ?", slug).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %s", ErrNotSubscribed, slug)
	}
	if err != nil {
		return "", err
	}
	token, err := s.key.Open(slug, sealed)
	if err != nil {
		return "", fmt.Errorf("%w: workspace %s", ErrUnreadable, slug)
	}
	return token, nil
}

// row is a *sql.Row or a *sql.Rows positioned on a row.
type row interface {
	Scan(dest ...any) error
}

// scan reads a subscription from a row of columns, with its revision. The state
// of the token comes from opening it.
func (s *Store) scan(r row) (Subscription, int64, error) {
	var (
		sub              Subscription
		sealed           []byte
		user             sql.NullString
		created, updated string
		revision         int64
	)
	if err := r.Scan(&sub.Slug, &sealed, &user, &sub.SubscribedBy, &created, &updated, &revision); err != nil {
		return Subscription{}, 0, err
	}
	var err error
	if sub.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return Subscription{}, 0, fmt.Errorf("subscription %s: created_at: %w", sub.Slug, err)
	}
	if sub.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return Subscription{}, 0, fmt.Errorf("subscription %s: updated_at: %w", sub.Slug, err)
	}
	sub.TokenUser = user.String
	sub.TokenState = TokenOK
	if _, err := s.key.Open(sub.Slug, sealed); err != nil {
		sub.TokenState = TokenUnreadable
	}
	return sub, revision, nil
}

// DB is the handle of the file, for code that reads it directly (the tests do).
func (s *Store) DB() *sql.DB { return s.db }

// Ping says whether the file can be queried.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close closes the file, which checkpoints the WAL.
func (s *Store) Close() error {
	return s.db.Close()
}
