package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TS-12-1 (unit): NewRun exposes a 32-lowercase-hex session_id and keeps its
// signature.
func TestTS12_1_NewRunExposesSessionID(t *testing.T) {
	r := NewRun("tool", "v1")
	id := r.SessionID()
	if len(id) != 32 {
		t.Fatalf("SessionID() length = %d, want 32", len(id))
	}
	re := regexp.MustCompile(`^[0-9a-f]{32}$`)
	if !re.MatchString(id) {
		t.Fatalf("SessionID() = %q, does not match ^[0-9a-f]{32}$", id)
	}
	// NewRun keeps its existing signature: (tool, version string) *Run
	var _ func(string, string) *Run = NewRun
}

// TS-12-2 (property): Session ids from distinct runs are well-formed and
// differ, with no pid/clock/host component.
func TestTS12_2_SessionIDsAreUniqueAndWellFormed(t *testing.T) {
	const N = 500
	re := regexp.MustCompile(`^[0-9a-f]{32}$`)
	ids := make(map[string]bool, N)
	pid := fmt.Sprintf("%x", os.Getpid())
	for i := 0; i < N; i++ {
		r := NewRun("tool", "v1")
		id := r.SessionID()
		if !re.MatchString(id) {
			t.Fatalf("run %d: SessionID() = %q, does not match ^[0-9a-f]{32}$", i, id)
		}
		if ids[id] {
			t.Fatalf("run %d: duplicate SessionID() = %q", i, id)
		}
		ids[id] = true
		// The first 8 chars should not be the hex of the pid (weak check).
		if len(pid) <= 8 && strings.HasPrefix(id, pid) {
			t.Errorf("run %d: SessionID() %q starts with hex(pid) %q", i, id, pid)
		}
	}
	if len(ids) != N {
		t.Fatalf("expected %d unique ids, got %d", N, len(ids))
	}
}

// TS-12-3 (integration): A flag-parse failure still emits an envelope with a
// session_id, on stdout and in the report.
func TestTS12_3_FlagParseFailureHasSessionID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	app, _ := newApp(t, nil)
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--bogus", "x"},
		strings.NewReader(""), &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	var env Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	re := regexp.MustCompile(`^[0-9a-f]{32}$`)
	if !re.MatchString(env.SessionID) {
		t.Fatalf("envelope session_id = %q, want 32 hex chars", env.SessionID)
	}
	if env.Error == nil || env.Error.Stage != "usage" {
		t.Fatalf("expected usage error, got %+v", env.Error)
	}
	// The report file should have the same session_id.
	if env.ReportFile == "" {
		t.Fatal("expected a report file for a flag-parse failure")
	}
	b, err := os.ReadFile(env.ReportFile)
	if err != nil {
		t.Fatalf("reading report file: %v", err)
	}
	var rf Envelope
	if err := json.Unmarshal(b, &rf); err != nil {
		t.Fatalf("report file is not JSON: %v", err)
	}
	if rf.SessionID != env.SessionID {
		t.Errorf("report session_id = %q, stdout session_id = %q", rf.SessionID, env.SessionID)
	}
	// The report file name should end with the session_id.
	base := filepath.Base(env.ReportFile)
	wantSuffix := env.SessionID + ".json"
	if !strings.HasSuffix(base, wantSuffix) {
		t.Errorf("report file name %q does not end with %q", base, wantSuffix)
	}
}

// TS-12-4 (unit): The session id generator uses crypto/rand only and adds no
// module.
func TestTS12_4_SessionIDUsesCryptoRandOnly(t *testing.T) {
	// Parse the source file that defines NewRun to check imports.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "envelope.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing envelope.go: %v", err)
	}
	var hasCryptoRand, hasMathRand bool
	for _, imp := range f.Imports {
		path := imp.Path.Value
		if path == `"crypto/rand"` {
			hasCryptoRand = true
		}
		if strings.Contains(path, "math/rand") {
			hasMathRand = true
		}
	}
	if !hasCryptoRand {
		t.Error("envelope.go does not import crypto/rand")
	}
	if hasMathRand {
		t.Error("envelope.go imports math/rand, which should not be used")
	}
	// Also check encoding/hex is imported.
	var hasHex bool
	f2, err := parser.ParseFile(fset, "envelope.go", nil, parser.ImportsOnly)
	if err == nil {
		for _, imp := range f2.Imports {
			if imp.Path.Value == `"encoding/hex"` {
				hasHex = true
			}
		}
	}
	if !hasHex {
		t.Error("envelope.go does not import encoding/hex")
	}
}

// TS-12-6 (integration): The envelope has a required session_id right after
// schema_version, identical on stdout and in the report, and described by
// --schema.
func TestTS12_6_EnvelopeSessionIDAfterSchemaVersion(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()

	// Successful run.
	app, _ := newApp(t, nil)
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", dir, "some text"},
		strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}

	// Check key order: session_id immediately after schema_version.
	keys := docKeys(t, stdout.Bytes())
	svIdx := -1
	for i, k := range keys {
		if k == "schema_version" {
			svIdx = i
			break
		}
	}
	if svIdx < 0 {
		t.Fatal("schema_version not found in stdout keys")
	}
	if svIdx+1 >= len(keys) || keys[svIdx+1] != "session_id" {
		t.Errorf("key after schema_version is %q, want session_id; keys = %v", keys[svIdx+1], keys)
	}

	// Check stdout and report have the same session_id.
	var env Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	re := regexp.MustCompile(`^[0-9a-f]{32}$`)
	if !re.MatchString(env.SessionID) {
		t.Fatalf("envelope session_id = %q, want 32 hex chars", env.SessionID)
	}
	if env.ReportFile == "" {
		t.Fatal("expected a report file")
	}
	b, err := os.ReadFile(env.ReportFile)
	if err != nil {
		t.Fatalf("reading report file: %v", err)
	}
	var rf Envelope
	if err := json.Unmarshal(b, &rf); err != nil {
		t.Fatalf("report file is not JSON: %v", err)
	}
	if rf.SessionID != env.SessionID {
		t.Errorf("report session_id = %q, stdout session_id = %q", rf.SessionID, env.SessionID)
	}

	// Usage error run: session_id still present.
	app2, _ := newApp(t, nil)
	var stdout2, stderr2 bytes.Buffer
	code2 := app2.Main(context.Background(), []string{"--detail", "wrong", "x"},
		strings.NewReader(""), &stdout2, &stderr2)
	if code2 != ExitUsage {
		t.Fatalf("code = %d", code2)
	}
	var env2 Envelope
	if err := json.Unmarshal(stdout2.Bytes(), &env2); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if !re.MatchString(env2.SessionID) {
		t.Errorf("usage error envelope session_id = %q, want 32 hex chars", env2.SessionID)
	}

	// --schema: session_id is described and in required.
	app3, _ := newApp(t, nil)
	var stdout3, stderr3 bytes.Buffer
	code3 := app3.Main(context.Background(), []string{"--schema"},
		strings.NewReader(""), &stdout3, &stderr3)
	if code3 != ExitOK {
		t.Fatalf("--schema code = %d", code3)
	}
	var schema struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(stdout3.Bytes(), &schema); err != nil {
		t.Fatalf("--schema output is not JSON: %v", err)
	}
	var resultDoc struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(schema.Result, &resultDoc); err != nil {
		t.Fatalf("result doc is not JSON: %v", err)
	}
	if _, ok := resultDoc.Properties["session_id"]; !ok {
		t.Error("--schema result document does not list session_id in properties")
	}
	found := false
	for _, r := range resultDoc.Required {
		if r == "session_id" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("--schema result document does not list session_id in required; required = %v", resultDoc.Required)
	}
}

// TS-12-9 (unit): DefaultReportPath and DefaultEventsPath share stem,
// timestamp format and state base.
func TestTS12_9_DefaultPathsSharingStemAndBase(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/s")
	started := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	sid := "abcdef0123456789abcdef0123456789"

	rp, err := DefaultReportPath("tool", started, sid)
	if err != nil {
		t.Fatalf("DefaultReportPath: %v", err)
	}
	ep, err := DefaultEventsPath("tool", started, sid)
	if err != nil {
		t.Fatalf("DefaultEventsPath: %v", err)
	}

	// Report path: /s/agent-fox/runs/tool-20250102T030405Z-<sid>.json
	wantReport := "/s/agent-fox/runs/tool-20250102T030405Z-" + sid + ".json"
	if rp != wantReport {
		t.Errorf("DefaultReportPath = %q, want %q", rp, wantReport)
	}

	// Events path: /s/agent-fox/events/tool-20250102T030405Z-<sid>.jsonl
	wantEvents := "/s/agent-fox/events/tool-20250102T030405Z-" + sid + ".jsonl"
	if ep != wantEvents {
		t.Errorf("DefaultEventsPath = %q, want %q", ep, wantEvents)
	}

	// Stems are identical.
	reportStem := strings.TrimSuffix(filepath.Base(rp), ".json")
	eventsStem := strings.TrimSuffix(filepath.Base(ep), ".jsonl")
	if reportStem != eventsStem {
		t.Errorf("stems differ: report %q, events %q", reportStem, eventsStem)
	}

	// No colon in the base name.
	if strings.Contains(filepath.Base(rp), ":") {
		t.Errorf("report file name %q contains a colon", filepath.Base(rp))
	}

	// Both bases derive from the same state directory.
	reportBase := filepath.Dir(filepath.Dir(rp))
	eventsBase := filepath.Dir(filepath.Dir(ep))
	if reportBase != eventsBase {
		t.Errorf("base dirs differ: report %q, events %q", reportBase, eventsBase)
	}
}

// TS-12-12 (unit): The state directory resolves from XDG_STATE_HOME, else
// from the home directory, with runs/ and events/ beneath.
func TestTS12_12_StateDirectoryResolution(t *testing.T) {
	// Case A: XDG_STATE_HOME=/x
	t.Run("XDG_STATE_HOME set", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "/x")
		base, err := stateHome()
		if err != nil {
			t.Fatalf("stateHome: %v", err)
		}
		if base != "/x" {
			t.Errorf("stateHome() = %q, want /x", base)
		}
		rp, _ := DefaultReportPath("tool", time.Now(), "aaaa")
		ep, _ := DefaultEventsPath("tool", time.Now(), "aaaa")
		if !strings.HasPrefix(rp, "/x/agent-fox/runs/") {
			t.Errorf("report path %q does not start with /x/agent-fox/runs/", rp)
		}
		if !strings.HasPrefix(ep, "/x/agent-fox/events/") {
			t.Errorf("events path %q does not start with /x/agent-fox/events/", ep)
		}
	})

	// Case B: XDG_STATE_HOME unset, HOME=/h
	t.Run("HOME fallback", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "/h")
		base, err := stateHome()
		if err != nil {
			t.Fatalf("stateHome: %v", err)
		}
		want := filepath.Join("/h", ".local", "state")
		if base != want {
			t.Errorf("stateHome() = %q, want %q", base, want)
		}
	})
}

// TS-12-13 (unit): An empty XDG_STATE_HOME is treated as unset.
func TestTS12_13_EmptyXDGStateHomeIsUnset(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", tmp)
	base, err := stateHome()
	if err != nil {
		t.Fatalf("stateHome: %v", err)
	}
	want := filepath.Join(tmp, ".local", "state")
	if base != want {
		t.Errorf("stateHome() = %q, want %q", base, want)
	}
}
