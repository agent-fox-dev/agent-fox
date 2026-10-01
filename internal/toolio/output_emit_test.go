package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// outputEnv isolates the state an App.Main run touches.
func outputEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// TS-08-6 (integration): the end-of-Main envelope is fully written to
// --output before any byte reaches stdout.
func TestTS08_6_OutputWrittenBeforeStdout(t *testing.T) {
	outputEnv(t)
	outPath := filepath.Join(t.TempDir(), "out.json")
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]string{"stage": "done"}, nil
	})

	var probed int
	var fileEnv Envelope
	var stdout bytes.Buffer
	probe := writerFunc(func(p []byte) (int, error) {
		if probed == 0 {
			data, err := os.ReadFile(outPath)
			if err != nil {
				t.Errorf("at first stdout write the --output file is unreadable: %v", err)
			} else if err := json.Unmarshal(data, &fileEnv); err != nil {
				t.Errorf("at first stdout write the --output file is not a complete envelope: %v", err)
			}
		}
		probed++
		return stdout.Write(p)
	})
	var stderr bytes.Buffer
	app.Main(context.Background(), []string{"--dir", t.TempDir(), "--output", outPath, "x"},
		strings.NewReader(""), probe, &stderr)
	if probed == 0 {
		t.Fatal("nothing was written to stdout")
	}
	var stdoutEnv Envelope
	if err := json.Unmarshal(stdout.Bytes(), &stdoutEnv); err != nil {
		t.Fatal(err)
	}
	if fileEnv.Tool != stdoutEnv.Tool || fileEnv.ExitCode != stdoutEnv.ExitCode ||
		!reflect.DeepEqual(fileEnv.Result, stdoutEnv.Result) {
		t.Errorf("file envelope %+v differs from stdout envelope %+v", fileEnv, stdoutEnv)
	}
	if fileEnv.Tool != "tool" {
		t.Errorf("Tool = %q", fileEnv.Tool)
	}
}

// TS-08-7 (unit): the flag-parsing-failure envelope is written to --output
// before stdout, when --output parsed before the bad flag.
func TestTS08_7_ParseFailureEnvelopeWrittenToOutput(t *testing.T) {
	outputEnv(t)
	outPath := filepath.Join(t.TempDir(), "out.json")
	app, _ := newApp(t, nil)
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--output", outPath, "--nope", "x"},
		strings.NewReader(""), &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("no --output file: %v", err)
	}
	if !bytes.Equal(data, stdout.Bytes()) {
		t.Errorf("file and stdout differ:\nfile:   %s\nstdout: %s", data, stdout.Bytes())
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.ExitCode != ExitUsage || env.Error == nil || env.Error.Category != "usage" {
		t.Errorf("envelope = %+v", env)
	}
}

// TS-08-8 (unit): Emit's marshal-failure fallback envelope is written to
// --output identically to stdout.
func TestTS08_8_MarshalFailureFallbackWrittenToOutput(t *testing.T) {
	r := NewRun("tool", "test")
	env := r.Envelope(ExitOK, map[string]any{"bad": make(chan int)}, nil)
	outPath := filepath.Join(t.TempDir(), "out.json")
	var buf bytes.Buffer
	code := EmitWithOutput(&buf, outPath, env)
	if code != ExitFailed {
		t.Fatalf("code = %d, want %d", code, ExitFailed)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("no --output file: %v", err)
	}
	if !bytes.Equal(got, buf.Bytes()) {
		t.Errorf("file and stdout differ:\nfile:   %s\nstdout: %s", got, buf.Bytes())
	}
	var fb Envelope
	if err := json.Unmarshal(got, &fb); err != nil {
		t.Fatal(err)
	}
	if fb.ExitCode != ExitFailed || fb.Error == nil || fb.Error.Stage != "emit" {
		t.Errorf("fallback = %+v", fb)
	}
}

// TS-08-9 (property): the --output file always carries exactly the envelope
// stdout receives, across exit codes, results and warnings.
func TestTS08_9_OutputEqualsStdoutAcrossOutcomes(t *testing.T) {
	outputEnv(t)
	type result struct {
		Items []string `json:"items"`
	}
	results := []any{nil, map[string]string{"k": "v"}, &result{Items: []string{"a", "b"}}}
	warnSets := [][]WarnCode{nil, {WarnInputTruncated}, {WarnInputTruncated, WarnCommentsUnreadable}}
	for _, code := range []int{ExitOK, ExitFailed, ExitUsage, ExitNeedsHuman, ExitUnverified} {
		for ri, res := range results {
			for wi, ws := range warnSets {
				name := fmt.Sprintf("code%d/result%d/warn%d", code, ri, wi)
				t.Run(name, func(t *testing.T) {
					app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
						for _, w := range ws {
							d.Run.Warn(w, "low", "warned")
						}
						var fail *ErrorInfo
						if code != ExitOK {
							fail = &ErrorInfo{Stage: "verify", Category: "internal", Message: "nope"}
						}
						return code, res, fail
					})
					outPath := filepath.Join(t.TempDir(), "out.json")
					var stdout, stderr bytes.Buffer
					app.Main(context.Background(), []string{"--dir", t.TempDir(), "--output", outPath, "x"},
						strings.NewReader(""), &stdout, &stderr)
					data, err := os.ReadFile(outPath)
					if err != nil {
						t.Fatalf("no --output file: %v", err)
					}
					var a, b any
					if err := json.Unmarshal(data, &a); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(stdout.Bytes(), &b); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(a, b) {
						t.Errorf("file != stdout:\nfile:   %s\nstdout: %s", data, stdout.Bytes())
					}
					if !bytes.Equal(data, stdout.Bytes()) {
						t.Errorf("file and stdout differ byte for byte")
					}
				})
			}
		}
	}
}

// TS-08-10 (property): a write to --output is atomic: no partial content is
// ever observable and no temp file survives.
func TestTS08_10_WriteAtomicNeverObservedPartial(t *testing.T) {
	for _, size := range []int{10, 4 << 10, 256 << 10, 4 << 20} {
		t.Run(fmt.Sprintf("size%d", size), func(t *testing.T) {
			dir := t.TempDir()
			outPath := filepath.Join(dir, "out.json")
			doc, err := json.Marshal(map[string]string{"pad": strings.Repeat("x", size)})
			if err != nil {
				t.Fatal(err)
			}
			// An earlier complete document is replaced, so a reader sees one
			// complete document or the other, never a mixture.
			if err := writeAtomic(outPath, []byte(`{"old":true}`)); err != nil {
				t.Fatal(err)
			}
			var partial atomic.Int32
			done := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-done:
						return
					default:
					}
					data, err := os.ReadFile(outPath)
					if err != nil {
						continue // nothing yet is allowed
					}
					if !json.Valid(data) {
						partial.Add(1)
					}
				}
			}()
			if err := writeAtomic(outPath, doc); err != nil {
				t.Fatal(err)
			}
			close(done)
			wg.Wait()
			if n := partial.Load(); n != 0 {
				t.Errorf("observed %d partial documents", n)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "out.json" {
				t.Errorf("directory holds %v, want only out.json", entries)
			}
			got, _ := os.ReadFile(outPath)
			if !bytes.Equal(got, doc) {
				t.Error("destination does not hold the document written")
			}
		})
	}
}

// TS-08-10 (property, failure half): writeAtomic removes its temp file when
// the rename cannot happen.
func TestTS08_10_WriteAtomicLeavesNoTempOnFailure(t *testing.T) {
	dir := t.TempDir()
	// The destination is a non-empty directory, so the rename fails.
	dest := filepath.Join(dir, "dest")
	if err := os.MkdirAll(filepath.Join(dest, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(dest, []byte("{}")); err == nil {
		t.Fatal("expected an error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory holds %v, want only dest", entries)
	}
}

// TS-08-11 (unit): a missing parent directory for --output is created at
// write time.
func TestTS08_11_MissingParentCreated(t *testing.T) {
	outputEnv(t)
	outPath := filepath.Join(t.TempDir(), "a", "b", "c", "out.json")
	app, _ := newApp(t, nil)
	var stdout, stderr bytes.Buffer
	app.Main(context.Background(), []string{"--dir", t.TempDir(), "--output", outPath, "x"},
		strings.NewReader(""), &stdout, &stderr)
	if _, err := os.Stat(filepath.Dir(outPath)); err != nil {
		t.Errorf("parent not created: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("no --output file: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
}

// TS-08-12 (unit): --output is written under --dry-run exactly as without it.
func TestTS08_12_OutputWrittenUnderDryRun(t *testing.T) {
	outputEnv(t)
	app, _ := newApp(t, nil)
	dir := t.TempDir()
	outA, outB := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	runApp(t, app, []string{"--dir", t.TempDir(), "--dry-run", "--output", outA, "x"}, "")
	runApp(t, app, []string{"--dir", t.TempDir(), "--output", outB, "x"}, "")
	var envs [2]Envelope
	for i, p := range []string{outA, outB} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if err := json.Unmarshal(data, &envs[i]); err != nil {
			t.Fatal(err)
		}
	}
	if envs[0].Tool != envs[1].Tool || !reflect.DeepEqual(envs[0].Result, envs[1].Result) ||
		!reflect.DeepEqual(envs[0].Warnings, envs[1].Warnings) {
		t.Errorf("dry-run envelope %+v differs from %+v", envs[0], envs[1])
	}
}

// TS-08-13 (unit): -h performs no --output validation and writes no file,
// even for a malformed --output value.
func TestTS08_13_HelpWritesNoOutput(t *testing.T) {
	outputEnv(t)
	cwd := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	app, _ := newApp(t, nil)
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"-h", "--output", "-"},
		strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Errorf("code = %d, want %d", code, ExitOK)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(cwd, "-")); err == nil {
		t.Error(`a file named "-" was created`)
	}
	if strings.Contains(stderr.String(), "--output cannot be") {
		t.Errorf("stderr reports an --output usage error: %s", stderr.String())
	}
}

// TS-08-14 (unit): a bare invocation on a terminal performs no --output
// validation and writes no file.
func TestTS08_14_BareOnTerminalWritesNoOutput(t *testing.T) {
	outputEnv(t)
	app, _ := newApp(t, nil)
	tty, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()
	existing := t.TempDir()
	var stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--output", existing},
		strings.NewReader(""), tty, &stderr)
	if code != ExitUsage {
		t.Errorf("code = %d, want %d", code, ExitUsage)
	}
	if strings.Contains(stderr.String(), "is a directory") {
		t.Errorf("stderr reports an --output error: %s", stderr.String())
	}
	if entries, _ := os.ReadDir(existing); len(entries) != 0 {
		t.Errorf("--output directory holds %v", entries)
	}
}

// TS-08-15 (property): --output never changes ok, status or exit_code,
// whether it is unset, written, or fails to write.
func TestTS08_15_OutputNeverChangesOutcome(t *testing.T) {
	outputEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{ExitOK, ExitFailed, ExitUsage, ExitNeedsHuman, ExitUnverified} {
		t.Run(fmt.Sprintf("code%d", code), func(t *testing.T) {
			var envs []Envelope
			var exits []int
			for _, out := range []string{
				"",
				filepath.Join(t.TempDir(), "out.json"),
				filepath.Join(blocker, "sub", "out.json"), // parent is a file: cannot be created
			} {
				app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
					var fail *ErrorInfo
					if code != ExitOK {
						fail = &ErrorInfo{Stage: "verify", Category: "internal", Message: "nope"}
					}
					return code, map[string]string{"k": "v"}, fail
				})
				argv := []string{"--dir", t.TempDir()}
				if out != "" {
					argv = append(argv, "--output", out)
				}
				env, exit, _ := runApp(t, app, append(argv, "x"), "")
				envs, exits = append(envs, env), append(exits, exit)
			}
			for i := 1; i < len(envs); i++ {
				if envs[i].OK != envs[0].OK || envs[i].Status != envs[0].Status ||
					envs[i].ExitCode != envs[0].ExitCode || exits[i] != exits[0] {
					t.Errorf("state %d: ok=%v status=%q exit_code=%d exit=%d; baseline ok=%v status=%q exit_code=%d exit=%d",
						i, envs[i].OK, envs[i].Status, envs[i].ExitCode, exits[i],
						envs[0].OK, envs[0].Status, envs[0].ExitCode, exits[0])
				}
			}
		})
	}
}
