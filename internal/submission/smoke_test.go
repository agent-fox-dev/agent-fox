package submission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TestSmokeABrainWithItsOwnHandsSubmits_TS_07_82 verifies TS-07-82
// (07-PATH-3): a brain with its own hands submits through the submit command
// and gets a done envelope. The run directory is a real one, made by the shell's
// own constructor, and the command is run by the shell.
// Verifies: 07-PATH-3, 07-REQ-5.1, 07-REQ-5.4
func TestSmokeABrainWithItsOwnHandsSubmits_TS_07_82(t *testing.T) {
	dir := t.TempDir()
	rd, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(rd.Path, "run.json")
	if err := os.WriteFile(run, fmt.Appendf(nil, `{"run_id":%q,"kind":"test","attempt":1,"nonce":"0123456789abcdef"}`, rd.ID), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &toolio.App{Version: "test"}
	a.Register(Command("agent submit", echoKind()))
	var out, errb bytes.Buffer

	code := a.Main(context.Background(), []string{"af", "agent", "submit", "--dir", dir, "--run-id", rd.ID}, strings.NewReader(`{ "summary": "from a brain with its own hands" }`), &out, &errb)

	var env struct {
		Status    string `json:"status"`
		Result    Result `json:"result"`
		Artifacts []struct{ Kind, Path string }
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not an envelope: %v\n%s", err, out.String())
	}
	path := filepath.Join(rd.Path, "submission.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if code != 0 || env.Status != "done" || errb.Len() != 0 {
		t.Fatalf("exit code %d, status %q, stderr %q\n%s", code, env.Status, errb.String(), out.String())
	}
	if env.Result != (Result{RunID: rd.ID, Kind: "test", Path: path, SHA256: file.PayloadSHA256}) {
		t.Errorf("result = %+v, want the run, the kind, %s and the payload's hash %s", env.Result, path, file.PayloadSHA256)
	}
	if !slices.ContainsFunc(env.Artifacts, func(a struct{ Kind, Path string }) bool { return a.Kind == "submission" && a.Path == path }) {
		t.Errorf("artifacts = %+v, want the submission", env.Artifacts)
	}
	if string(file.Payload) != `{"summary":"from a brain with its own hands"}` || file.Nonce != "0123456789abcdef" || file.Attempt != 1 {
		t.Errorf("submission.json = %s", data)
	}
	entries, _ := os.ReadDir(rd.Path)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp") {
			t.Errorf("a temporary file is left: %s", e.Name())
		}
	}
}
