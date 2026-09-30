package toolio

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// fakeArtifactResult is a minimal Result stand-in implementing
// ArtifactsProvider, for exercising App.emit's wiring independent of any
// one tool's own Result type.
type fakeArtifactResult struct {
	Stage string `json:"stage"`
}

func (fakeArtifactResult) Artifacts() []Artifact {
	return []Artifact{{Kind: ArtifactBranch, Name: "fix/example", Base: "main"}}
}

// 06-REQ-4: App.emit populates Envelope.Artifacts from a Result that
// implements ArtifactsProvider, and appends a report_file entry once the
// report path is known.
func TestAppPopulatesArtifactsFromArtifactsProviderAndAppendsReportFile(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	dir := t.TempDir()
	report := dir + "/report.json"

	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		return ExitOK, &fakeArtifactResult{Stage: "done"}, nil
	})

	env, code, _ := runApp(t, app, []string{"--dir", dir, "--report-file", report, "some input"}, "")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}

	var sawBranch, sawReportFile bool
	for _, a := range env.Artifacts {
		switch a.Kind {
		case ArtifactBranch:
			sawBranch = true
			b, _ := json.Marshal(a)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if m["name"] != "fix/example" || m["base"] != "main" {
				t.Errorf("branch artifact = %v", m)
			}
		case ArtifactReportFile:
			sawReportFile = true
			b, _ := json.Marshal(a)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if m["path"] != report {
				t.Errorf("report_file artifact path = %v, want %q", m["path"], report)
			}
		}
	}
	if !sawBranch {
		t.Errorf("expected a branch artifact in %+v", env.Artifacts)
	}
	if !sawReportFile {
		t.Errorf("expected a report_file artifact in %+v", env.Artifacts)
	}

	// The report file itself carries the same artifacts (the full view,
	// independent of --detail).
	written := readReportFile(t, report)
	if len(written.Artifacts) != len(env.Artifacts) {
		t.Errorf("report file artifacts = %+v, want %+v", written.Artifacts, env.Artifacts)
	}
}

func readReportFile(t *testing.T, path string) Envelope {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading report file: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("unmarshal report file: %v", err)
	}
	return env
}
