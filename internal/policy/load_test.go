package policy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// TestLoadPolicy_TakesTheFirstSourceThatExists: the file named, then
// guard.json, then the defaults with the suite detection finds; a key a file
// does not have takes its default.
func TestLoadPolicy_TakesTheFirstSourceThatExists(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "Makefile", "check:\n\ttrue\n")
	named := testutil.WriteFile(t, t.TempDir(), "policy.json", `{"version":1,"suite":[["foo"]]}`)
	guard := testutil.WriteFile(t, dir, ".agent-fox/guard.json", `{"version":1,"protected_paths":["docs/"]}`)
	detected := [][]string{{"make", "check"}}

	if p, err := LoadPolicy(ctx, dir, named); err != nil || !reflect.DeepEqual(p.Suite, [][]string{{"foo"}}) || !slices.Equal(p.ProtectedPaths, DefaultProtectedPaths()) || len(p.AllowPrograms) != 0 {
		t.Errorf("the named file gave %+v, %v: want its suite, the default protected paths and no allowlist, although guard.json exists", p, err)
	}
	if p, err := LoadPolicy(ctx, dir, ""); err != nil || !slices.Equal(p.ProtectedPaths, []string{"docs/"}) || !reflect.DeepEqual(p.Suite, detected) {
		t.Errorf("guard.json gave %+v, %v: want docs/ and the detected suite", p, err)
	}
	if err := os.Remove(guard); err != nil {
		t.Fatal(err)
	}
	if p, err := LoadPolicy(ctx, dir, ""); err != nil || !slices.Equal(p.ProtectedPaths, []string{".git/", ".agent-fox/", ".specs/", ".github/workflows/", ".gitlab-ci.yml"}) || !reflect.DeepEqual(p.Suite, detected) || len(p.AllowPrograms) != 0 {
		t.Errorf("the defaults are %+v, %v", p, err)
	}
}

// TestLoadPolicy_TakesAKeyThatIsThereAsItIs: an empty list included, and with
// no keys at all every one takes its default.
func TestLoadPolicy_TakesAKeyThatIsThereAsItIs(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "Makefile", "check:\n\ttrue\n")
	testutil.WriteFile(t, dir, ".agent-fox/guard.json", `{"version":1,"suite":[["a","b"],["c"]],"protected_paths":[],"allow_programs":["go","ls"]}`)

	p, err := LoadPolicy(context.Background(), dir, "")
	if err != nil || !reflect.DeepEqual(p.Suite, [][]string{{"a", "b"}, {"c"}}) || len(p.ProtectedPaths) != 0 || !slices.Equal(p.AllowPrograms, []string{"go", "ls"}) {
		t.Errorf("policy = %+v, %v; want the file's keys, and no protected paths because the file says none", p, err)
	}

	testutil.WriteFile(t, dir, ".agent-fox/guard.json", `{"version":1}`)
	if p, err = LoadPolicy(context.Background(), dir, ""); err != nil || !slices.Equal(p.ProtectedPaths, DefaultProtectedPaths()) || !reflect.DeepEqual(p.Suite, [][]string{{"make", "check"}}) {
		t.Errorf("a file with no keys gave %+v, %v", p, err)
	}
}

// TestLoadPolicy_RefusesAFileThatCannotBeUsed: it is a *ConfigError that
// names the file, and nothing is decided with a policy half read.
func TestLoadPolicy_RefusesAFileThatCannotBeUsed(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":                `{not json`,
		"no version":              `{"suite":[["a"]]}`,
		"another version":         `{"version":2}`,
		"an unknown key":          `{"version":1,"protected_path":[".git/"]}`,
		"a suite of strings":      `{"version":1,"suite":["make check"]}`,
		"an empty form":           `{"version":1,"suite":[[]]}`,
		"a path above":            `{"version":1,"protected_paths":["../x"]}`,
		"the checkout":            `{"version":1,"protected_paths":["."]}`,
		"an absolute path":        `{"version":1,"protected_paths":["/etc"]}`,
		"an empty path":           `{"version":1,"protected_paths":[""]}`,
		"programs of a string":    `{"version":1,"allow_programs":"go"}`,
		"an empty program":        `{"version":1,"allow_programs":[""]}`,
		"two documents":           `{"version":1} {"version":1}`,
		"an empty file":           ``,
		"a directory, not a file": "",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, ".agent-fox", "guard.json")
			if name == "a directory, not a file" {
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				testutil.WriteFile(t, dir, ".agent-fox/guard.json", content)
			}

			_, err := LoadPolicy(context.Background(), dir, "")

			var config *ConfigError
			if !errors.As(err, &config) || config.Path != path || !strings.Contains(err.Error(), path) {
				t.Errorf("LoadPolicy = %v, want a *ConfigError naming %s", err, path)
			}
		})
	}
}

// TestLoadPolicy_ReadsOnlyTheSourceItTakes: a broken guard.json beside a
// named file is not read, a named file that is not there is an error, and the
// detection of the defaults fails with a gate.json that cannot be used, and
// with a checkout that is not there.
func TestLoadPolicy_ReadsOnlyTheSourceItTakes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	testutil.WriteFile(t, dir, ".agent-fox/guard.json", `{broken`)
	good := testutil.WriteFile(t, t.TempDir(), "p.json", `{"version":1,"suite":[["x"]]}`)
	var config *ConfigError

	if _, err := LoadPolicy(ctx, dir, good); err != nil {
		t.Errorf("a named file with a broken guard.json beside it: %v", err)
	}
	if _, err := LoadPolicy(ctx, dir, filepath.Join(dir, "missing.json")); !errors.As(err, &config) || !strings.Contains(err.Error(), "missing.json") {
		t.Errorf("a named file that is not there: %v, want a *ConfigError naming it", err)
	}

	bad := t.TempDir()
	testutil.WriteFile(t, bad, ".agent-fox/gate.json", `{broken`)
	if _, err := LoadPolicy(ctx, bad, ""); !errors.As(err, &config) || !strings.Contains(err.Error(), "gate.json") || strings.Contains(config.Message, "invalid gate configuration") {
		t.Errorf("defaults with a broken gate.json: %v, want a config error naming it", err)
	}
	if _, err := LoadPolicy(ctx, bad, good); err != nil {
		t.Errorf("a policy file that does not need detection: %v", err)
	}
	if _, err := LoadPolicy(ctx, filepath.Join(bad, "missing"), ""); err == nil || errors.As(err, &config) {
		t.Errorf("defaults for a checkout that is not there: %v, want the error of the file view", err)
	}
}

// TestMarshalGuardFile_IsThePolicyLoadPolicyReadsBack: every key is written,
// an empty list as an empty list, with the version and no null.
func TestMarshalGuardFile_IsThePolicyLoadPolicyReadsBack(t *testing.T) {
	for name, p := range map[string]Policy{
		"a full policy": {Suite: [][]string{{"make", "check"}, {"go", "test", "./..."}}, ProtectedPaths: DefaultProtectedPaths(), AllowPrograms: []string{"git", "make"}},
		"empty lists":   {Suite: [][]string{{"make", "test"}}, ProtectedPaths: []string{}, AllowPrograms: []string{}},
		"no lists":      {},
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := MarshalGuardFile(p)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			testutil.WriteFile(t, dir, ".agent-fox/guard.json", string(doc))

			got, err := LoadPolicy(context.Background(), dir, "")

			if err != nil {
				t.Fatalf("LoadPolicy of %s: %v", doc, err)
			}
			if len(got.Suite) != len(p.Suite) || len(got.Suite) > 0 && !reflect.DeepEqual(got.Suite, p.Suite) || !slices.Equal(got.ProtectedPaths, p.ProtectedPaths) || !slices.Equal(got.AllowPrograms, p.AllowPrograms) {
				t.Errorf("read back %+v from %s, want %+v", got, doc, p)
			}
			if !strings.Contains(string(doc), `"version": 1`) || strings.Contains(string(doc), "null") {
				t.Errorf("guard.json = %s, want the version and no null", doc)
			}
		})
	}
}
