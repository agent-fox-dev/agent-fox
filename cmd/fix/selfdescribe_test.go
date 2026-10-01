package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// runStdout runs a tool's binary with the given args and returns its parsed
// stdout object. It uses a minimal environment (no credentials, no model) and
// a throwaway state dir, because a usage-error envelope writes a report file.
func runStdout(t *testing.T, tool string, args ...string) map[string]any {
	t.Helper()
	bin := getTestBin(t, tool)
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"XDG_STATE_HOME=" + t.TempDir(),
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("%s %v: stdout is not one JSON object: %v\nstdout: %s\nstderr: %s", tool, args, err, stdout.String(), stderr.String())
	}
	return doc
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var allTools = []string{"spec", "issue", "fix", "impl"}

// TS-09-8 (integration): the document carries exactly the seven top-level
// keys, with tool and schema_version matching an ordinary envelope.
func TestTS09_8_SelfDescriptionKeysMatchEnvelope(t *testing.T) {
	doc := runStdout(t, "fix", "--schema")
	want := []string{"description", "exit_codes", "flags", "input", "result", "schema_version", "tool"}
	if got := sortedKeys(doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("top-level keys = %v, want %v", got, want)
	}
	env := runStdout(t, "fix", "--land", "bogus", "text")
	if doc["tool"] != env["tool"] {
		t.Errorf("tool: document %v, envelope %v", doc["tool"], env["tool"])
	}
	if doc["schema_version"] == nil || doc["schema_version"] != env["schema_version"] {
		t.Errorf("schema_version: document %v, envelope %v", doc["schema_version"], env["schema_version"])
	}
}

// TS-09-9 (integration): each tool's description is its own one sentence.
func TestTS09_9_DescriptionsDistinctSingleSentences(t *testing.T) {
	seen := map[string]string{}
	for _, tool := range allTools {
		d, _ := runStdout(t, tool, "--schema")["description"].(string)
		if strings.TrimSpace(d) == "" || strings.Contains(d, "\n") {
			t.Errorf("%s: description %q must be non-empty and single-line", tool, d)
		}
		if other, dup := seen[d]; dup {
			t.Errorf("%s and %s share description %q", tool, other, d)
		}
		seen[d] = tool
	}
	if len(seen) != 4 {
		t.Errorf("want 4 distinct descriptions, got %d", len(seen))
	}
}

// TS-09-10 (integration): every tool's input carries the same closed kinds
// list, impl included, and a non-empty description.
func TestTS09_10_InputKindsClosedList(t *testing.T) {
	want := []any{"text", "file", "stdin", "issue"}
	for _, tool := range allTools {
		in, _ := runStdout(t, tool, "--schema")["input"].(map[string]any)
		if in == nil {
			t.Fatalf("%s: no input object", tool)
		}
		if !reflect.DeepEqual(in["kinds"], want) {
			t.Errorf("%s: input.kinds = %v, want %v", tool, in["kinds"], want)
		}
		if d, _ := in["description"].(string); strings.TrimSpace(d) == "" {
			t.Errorf("%s: input.description is empty", tool)
		}
	}
}

func exitCodeKeys(t *testing.T, tool string) []string {
	t.Helper()
	ec, _ := runStdout(t, tool, "--schema")["exit_codes"].(map[string]any)
	for k, v := range ec {
		s, _ := v.(string)
		if strings.TrimSpace(s) == "" || strings.Contains(s, "\n") {
			t.Errorf("%s: exit_codes[%s] = %q must be a non-empty single line", tool, k, s)
		}
	}
	return sortedKeys(ec)
}

// TS-09-11 (integration): fix's exit_codes lists exactly 0..4, each described.
func TestTS09_11_FixExitCodes(t *testing.T) {
	want := []string{"0", "1", "2", "3", "4"}
	if got := exitCodeKeys(t, "fix"); !reflect.DeepEqual(got, want) {
		t.Errorf("fix exit_codes keys = %v, want %v", got, want)
	}
}

// TS-09-12 (integration): issue and spec each report exactly 0, 1 and 2.
func TestTS09_12_IssueSpecExitCodes(t *testing.T) {
	want := []string{"0", "1", "2"}
	for _, tool := range []string{"issue", "spec"} {
		if got := exitCodeKeys(t, tool); !reflect.DeepEqual(got, want) {
			t.Errorf("%s exit_codes keys = %v, want %v", tool, got, want)
		}
	}
}

// TS-09-13 (integration): fix and impl each report exactly 0 through 4.
func TestTS09_13_FixImplExitCodes(t *testing.T) {
	want := []string{"0", "1", "2", "3", "4"}
	for _, tool := range []string{"fix", "impl"} {
		if got := exitCodeKeys(t, tool); !reflect.DeepEqual(got, want) {
			t.Errorf("%s exit_codes keys = %v, want %v", tool, got, want)
		}
	}
}
