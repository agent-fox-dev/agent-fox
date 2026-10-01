package main

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/schematest"
)

// TS-09-44 (smoke): A caller introspects fix's interface with no credential
// configured before ever running it.
//
// Verifies: 09-PATH-1, 09-REQ-1.2, 09-REQ-1.7, 09-REQ-2.1
// Real components: the built cmd/fix binary, toolio.App, toolio.Common, the
// flags generator, the result generator, santhosh-tekuri/jsonschema/v6
func TestTS09_44_CallerIntrospectsFixWithNoCredential_Smoke(t *testing.T) {
	bin := schematest.Build(t, "fix")
	empty := t.TempDir()

	// An environment with no credential, no model selection, no forge token,
	// and no way to find a home directory's configuration.
	cmd := exec.Command(bin, "--dir", empty, "--schema")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "XDG_STATE_HOME=" + t.TempDir()}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("fix --dir <empty> --schema: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	// stdout is exactly one JSON document.
	var doc map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\n%s", err, stdout.String())
	}
	if dec.More() {
		t.Errorf("stdout holds more than one JSON document:\n%s", stdout.String())
	}
	want := []string{"description", "exit_codes", "flags", "input", "result", "schema_version", "tool"}
	got := make([]string, 0, len(doc))
	for k := range doc {
		got = append(got, k)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("top-level keys = %v, want %v", got, want)
	}
	if string(doc["tool"]) != `"fix"` || string(doc["schema_version"]) != `"2.0.0"` {
		t.Errorf("tool = %s, schema_version = %s", doc["tool"], doc["schema_version"])
	}

	// flags and result both compile against the 2020-12 meta-schema.
	if err := schematest.CompileDocuments(stdout.Bytes()); err != nil {
		t.Errorf("%v", err)
	}

	// Nothing was resolved, written or reported: the tool never reached a
	// model, so it had nothing to say on stderr, and the directory it was
	// pointed at is as empty as it was.
	if stderr.Len() != 0 {
		t.Errorf("stderr is not empty:\n%s", stderr.String())
	}
	if entries, err := os.ReadDir(empty); err != nil || len(entries) != 0 {
		t.Errorf("--dir was touched: %v (err %v)", entries, err)
	}
}

// copyTree copies the module at src to dst, leaving out version control and
// build output. Symlinks are not followed or recreated; none are needed.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			switch rel {
			case ".git", "bin", "dist":
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := d.Info()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(dst, rel), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
	if err != nil {
		t.Fatalf("copying the working tree: %v", err)
	}
}

// innerRunEnv marks the `make test` a smoke test starts inside its own copy of
// the tree, so that copy's TS-09-45 does not start another one.
const innerRunEnv = "AGENTFOX_TS0945_INNER"

// verifySubtestEnv is the marker the root package's TS-04-35 sets on the
// `make test` it starts. TS-09-45 honours it in both directions: it does not
// start its own two runs inside TS-04-35's, and it sets it on its own runs so
// the copy's TS-04-35 does not start a third. Without this the recursive
// suites multiply and run side by side, and the load alone fails the
// suite's timing-sensitive tests.
const verifySubtestEnv = "AF_VERIFY_SUBTEST"

// TS-09-45 (smoke): A maintainer's undeclared Result field change is caught by
// the golden-file test.
//
// Verifies: 09-PATH-2, 09-REQ-6.1, 09-REQ-6.2
// Real components: cmd/fix main, cmd/fix's golden-file test, codefix.Result,
// the result generator, make test / go test
func TestTS09_45_UndeclaredResultFieldChangeFailsMakeTest_Smoke(t *testing.T) {
	if testing.Short() {
		t.Skip("runs make test on a copy of the repository")
	}
	if os.Getenv(innerRunEnv) != "" {
		t.Skip("already inside the copy this test makes")
	}
	if os.Getenv(verifySubtestEnv) == "1" {
		t.Skip("already inside a recursive make test; the outer run covers this")
	}
	root := schematest.Root(t)

	// The copy lives next to a link to agentkit-go, which go.mod reaches as
	// ../agentkit-go.
	base := t.TempDir()
	kit, err := filepath.Abs(filepath.Join(root, "..", "agentkit-go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(kit, filepath.Join(base, "agentkit-go")); err != nil {
		t.Skipf("cannot link agentkit-go: %v", err)
	}
	work := filepath.Join(base, "agent-fox")
	copyTree(t, root, work)

	makeTest := func() (string, error) {
		cmd := exec.Command("make", "test")
		cmd.Dir = work
		cmd.Env = append(os.Environ(), innerRunEnv+"=1", verifySubtestEnv+"=1")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// A maintainer adds a tagged field to codefix.Result and does not touch
	// the golden file.
	types := filepath.Join(work, "codefix", "types.go")
	orig, err := os.ReadFile(types)
	if err != nil {
		t.Fatal(err)
	}
	const anchor = "type Result struct {\n"
	if !bytes.Contains(orig, []byte(anchor)) {
		t.Fatalf("codefix/types.go has no %q to mutate", anchor)
	}
	mutated := bytes.Replace(orig, []byte(anchor), []byte(anchor+
		"\tSmokeProbe string `json:\"smoke_probe,omitempty\" description:\"A field added without regenerating the golden file.\"`\n"), 1)
	if err := os.WriteFile(types, mutated, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := makeTest()
	if err == nil {
		t.Fatalf("make test passed with an undeclared Result field:\n%s", out)
	}
	for _, want := range []string{"cmd/fix", "TestSchemaGolden", "differs from the golden file", "smoke_probe"} {
		if !strings.Contains(out, want) {
			t.Errorf("make test output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "diff (-golden +live)") {
		t.Errorf("make test output shows no diff between the live output and the golden file:\n%s", out)
	}

	// Restoring codefix.Result makes it pass again.
	if err := os.WriteFile(types, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := makeTest(); err != nil {
		t.Fatalf("make test fails after codefix.Result is restored: %v\n%s", err, out)
	}
}
