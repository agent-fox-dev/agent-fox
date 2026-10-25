package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestOpen_AppliesEachMigrationOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-fox.db")
	var want []string
	for v := range migrations {
		want = append(want, fmt.Sprint(v+1))
	}
	for range 2 {
		s, err := Open(ctx, path, keyOf(t, false))
		if err != nil {
			t.Fatal(err)
		}
		if got := testutil.Column(t, s.DB(), "SELECT version FROM af_schema_migrations ORDER BY version"); !slices.Equal(got, want) {
			t.Errorf("the migration record lists %v, want %v", got, want)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrate_AFailingMigrationLeavesNoPartialChangeAndTheNextStartGoesOn(t *testing.T) {
	db := testutil.OpenDB(t)
	steps := []string{
		"CREATE TABLE af_first (x INTEGER)",
		"CREATE TABLE af_partial (x INTEGER); INSERT INTO af_does_not_exist VALUES (1)",
		"CREATE TABLE af_third (x INTEGER)",
	}
	if err := migrate(ctx, db, steps, at(0)); err == nil || !strings.Contains(err.Error(), "migration 2") {
		t.Fatalf("migrate = %v, want migration 2 to fail", err)
	}
	if got := testutil.Column(t, db, "SELECT version FROM af_schema_migrations"); !slices.Equal(got, []string{"1"}) {
		t.Errorf("the record lists %v, want only the migration that committed", got)
	}
	tables := testutil.Column(t, db, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if slices.Contains(tables, "af_partial") || slices.Contains(tables, "af_third") || !slices.Contains(tables, "af_first") {
		t.Errorf("tables = %v, want af_first and neither af_partial (rolled back) nor af_third (never reached)", tables)
	}
	steps[1] = "CREATE TABLE af_second (x INTEGER)"
	if err := migrate(ctx, db, steps, at(1)); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Column(t, db, "SELECT version FROM af_schema_migrations ORDER BY version"); !slices.Equal(got, []string{"1", "2", "3"}) {
		t.Errorf("the record lists %v, want 1, 2 and 3", got)
	}
}

func TestOpen_RefusesADatabaseOfANewerBinaryAndLeavesItAlone(t *testing.T) {
	s, path := newStore(t)
	testutil.Exec(t, s.DB(), "INSERT INTO af_schema_migrations(version, applied_at) VALUES (999, 'x')")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before := testutil.SHA256(testutil.ReadFile(t, path))
	_, err := Open(ctx, path, keyOf(t, false))
	for _, want := range []string{"999", fmt.Sprint(len(migrations)), path} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %s", err, want)
		}
	}
	if testutil.SHA256(testutil.ReadFile(t, path)) != before {
		t.Error("the file changed although the open was refused")
	}
}

func TestOpen_LeavesTheTablesOfOtherModulesAndUserVersionAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{"CREATE TABLE jobs (id INTEGER PRIMARY KEY, payload TEXT)", "INSERT INTO jobs(payload) VALUES ('one'), ('two')", "PRAGMA user_version = 42"} {
		testutil.Exec(t, raw, stmt)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	s := open(t, path, keyOf(t, false))
	if _, _, err := s.Upsert(ctx, "widgets", "tok", "", "usr_1", at(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete(ctx, "widgets"); err != nil {
		t.Fatal(err)
	}
	for _, name := range testutil.Column(t, s.DB(), "SELECT name FROM sqlite_master WHERE type = 'table'") {
		if name != "jobs" && !strings.HasPrefix(name, "af_") {
			t.Errorf("table %q is neither the other module's nor af_-prefixed", name)
		}
	}
	if got := testutil.Column(t, s.DB(), "PRAGMA user_version"); !slices.Equal(got, []string{"42"}) {
		t.Errorf("user_version = %v, want 42, untouched", got)
	}
	if got := testutil.Column(t, s.DB(), "SELECT payload FROM jobs ORDER BY id"); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("the other module's table holds %v", got)
	}
}

func TestOpen_FailuresNameTheFile(t *testing.T) {
	root := t.TempDir()
	file := testutil.WriteFile(t, root, "plain-file", "x")
	garbage := testutil.WriteFile(t, root, "garbage.db", strings.Repeat("not a database ", 200))
	if err := os.Chmod(garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"a parent that is a regular file": filepath.Join(file, "agent-fox.db"),
		"a file that is not a database":   garbage,
		"a directory":                     root,
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Open(ctx, path, keyOf(t, false))
			if err == nil {
				_ = s.Close()
				t.Fatal("Open succeeded")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want it to name %s", err, path)
			}
		})
	}
}
