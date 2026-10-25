package toolio

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFlags_MayStandBeforeOrAfterTheArgument(t *testing.T) {
	dir := t.TempDir()
	wd, _ := filepath.Abs(".")
	for _, tc := range []struct {
		args    []string
		pos     []string
		dir     string
		context []string
	}{
		{[]string{"./bug.md", "--dir", dir}, []string{"./bug.md"}, dir, nil},
		{[]string{"--dir", dir, "./bug.md"}, []string{"./bug.md"}, dir, nil},
		{[]string{"--dir=" + dir, "./bug.md"}, []string{"./bug.md"}, dir, nil},
		{[]string{"-dir", dir}, nil, dir, nil},
		{[]string{"--", "--dir"}, []string{"--dir"}, wd, nil},
		{[]string{"-"}, []string{"-"}, wd, nil},
		// The word after a flag that takes a value is its value, whatever it looks like.
		{[]string{"text", "--context", "-x", "--context", "--"}, []string{"text"}, wd, []string{"-x", "--"}},
		{[]string{"--context", " B ", "--context", "x y", "--context", "B"}, nil, wd, []string{" B ", "x y", "B"}},
	} {
		var rec recorder
		app := newApp()
		app.Register(rec.command(Command{Path: "X", Dir: true, Context: true}))

		r := run(t, app, append([]string{"X"}, tc.args...)...)

		if r.code != 0 || rec.ran != 1 || !slices.Equal(rec.last.Args, tc.pos) || rec.last.Dir != tc.dir || !slices.Equal(rec.last.Context, tc.context) {
			t.Errorf("%q: exit %d, ran %d, args %q, dir %q, context %q; want %q, %q, %q", tc.args, r.code, rec.ran, rec.last.Args, rec.last.Dir, rec.last.Context, tc.pos, tc.dir, tc.context)
		}
	}
}

func TestFlags_ABadInvocationIsUsageAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"an unknown flag", []string{"X", "--bogus", "a"}, "--bogus"},
		{"an extra argument", []string{"X", "a", "b"}, `"b"`},
		{"an argument to a command that takes none", []string{"N", "a"}, "takes no argument"},
		{"a flag without its value", []string{"X", "a", "--dir"}, "--dir"},
		{"a duration that is none", []string{"X", "--timeout", "abc"}, "timeout"},
		{"a duration without a unit", []string{"X", "--timeout", "5"}, "timeout"},
		{"a negative duration", []string{"X", "--timeout", "-5s"}, "negative"},
		{"an empty --context", []string{"X", "--context", ""}, "empty"},
		{"an empty --context=", []string{"X", "--context="}, "empty"},
		{"a later empty --context", []string{"X", "--context", "A", "--context", ""}, "empty"},
		{"--context to a command that did not opt in", []string{"N", "--context", "A"}, "--context"},
		{"--timeout to a command that did not opt in", []string{"N", "--timeout", "1s"}, "--timeout"},
		{"--dry-run to a command that did not opt in", []string{"N", "--dry-run"}, "--dry-run"},
		{"--repo to a command that did not opt in", []string{"N", "--repo", "o/r"}, "--repo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rec recorder
			app := newApp()
			app.Register(rec.command(Command{Path: "X", Dir: true, Context: true, Timeout: true, MaxArgs: 1}))
			app.Register(rec.command(Command{Path: "N", MaxArgs: NoArgs}))

			var r result
			// The flag package writes to the process's stderr unless it is told not to.
			processStderr := captureStderr(t, func() { r = run(t, app, tc.args...) })
			env := r.env(t)

			if r.code != 2 || env["status"] != "usage" || !strings.Contains(at(env, "error", "message").(string), tc.want) || rec.ran != 0 {
				t.Errorf("exit %d, ran %d, envelope %s; want usage naming %s", r.code, rec.ran, r.stdout, tc.want)
			}
			if r.stderr != "" || processStderr != "" {
				t.Errorf("stderr %q, the process's stderr %q; want nothing", r.stderr, processStderr)
			}
		})
	}
}

// captureStderr is what f writes to the process's os.Stderr.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = saved
	_ = w.Close()
	data, _ := io.ReadAll(r)
	return string(data)
}
