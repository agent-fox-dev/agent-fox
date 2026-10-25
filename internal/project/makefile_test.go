package project

import (
	"reflect"
	"testing"
)

// TestDetect_AMakefileGivesTheGate: without gate.json, make check when the
// Makefile has it, else make lint and make test, ahead of the manifests; only
// the rules of the Makefile count as targets.
func TestDetect_AMakefileGivesTheGate(t *testing.T) {
	check := []Check{{Name: "make check", Argv: []string{"make", "check"}, Kind: KindCheck, Source: "Makefile:check"}}
	lintTest := []Check{
		{Name: "make lint", Argv: []string{"make", "lint"}, Kind: KindLint, Source: "Makefile:lint"},
		{Name: "make test", Argv: []string{"make", "test"}, Kind: KindTest, Source: "Makefile:test"},
	}
	goChecks := []Check{
		{Name: "go vet ./...", Argv: []string{"go", "vet", "./..."}, Kind: KindLint, Source: "go.mod"},
		{Name: "go test ./...", Argv: []string{"go", "test", "./..."}, Kind: KindTest, Source: "go.mod"},
	}
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []Check
	}{
		{"a check target", map[string]string{"Makefile": "check: lint test\n\t@true\n", "go.mod": "module x\n"}, check},
		{"test and lint in a GNUmakefile", map[string]string{"GNUmakefile": "test:\n\ttrue\nlint:\n\ttrue\n"},
			[]Check{
				{Name: "make lint", Argv: []string{"make", "lint"}, Kind: KindLint, Source: "GNUmakefile:lint"},
				{Name: "make test", Argv: []string{"make", "test"}, Kind: KindTest, Source: "GNUmakefile:test"},
			}},
		{"the GNUmakefile before the Makefile", map[string]string{"GNUmakefile": "test:\n", "Makefile": "check:\n"},
			[]Check{{Name: "make test", Argv: []string{"make", "test"}, Kind: KindTest, Source: "GNUmakefile:test"}}},
		{"test only in a makefile", map[string]string{"makefile": "test:\n\ttrue\n"},
			[]Check{{Name: "make test", Argv: []string{"make", "test"}, Kind: KindTest, Source: "makefile:test"}}},
		{"lint without test is no gate", map[string]string{"Makefile": "lint:\n", "go.mod": "module x\n"}, goChecks},
		{"several targets before the colon", map[string]string{"Makefile": "all lint test: deps\n\ttrue\n"}, lintTest},
		{"a variable set for one target", map[string]string{"Makefile": "check: FLAGS=-v\r\ncheck:\r\n\ttrue\r\n"}, check},
		{"assignments and .PHONY are not targets", map[string]string{"go.mod": "module x\n", "Makefile": "VAR := x\nOTHER ::= y\nURL = http://x:80\nexport Z := 1\n.PHONY: check test\n"}, goChecks},
		{"nor recipes, comments, patterns and variables", map[string]string{"go.mod": "module x\n", "Makefile": "build:\n\techo check: done\n# check: no\n%.o: %.c\n$(NAME): x\n  \n"}, goChecks},
		{"nor a continued line or the body of a define", map[string]string{"go.mod": "module x\n", "Makefile": "OBJS = a \\\n  check: b\ndefine RULE\ncheck:\ndefine\tINNER\nendef\ntest:\nendef\n"}, goChecks},
		{"the rules after a define", map[string]string{"Makefile": "define X\nendef\nlint:\ntest:\n"}, lintTest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := detect(t, tc.files)
			if got := unavailable(d.Gate); got.Source != SourceDetected || !reflect.DeepEqual(got.Checks, tc.want) {
				t.Errorf("gate = %+v, want detected %+v", got, tc.want)
			}
		})
	}
}
