package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
)

// The kinds of a check.
const (
	KindLint  = "lint"
	KindTest  = "test"
	KindCheck = "check" // lint and test together
)

// The sources of a table.
const (
	SourceFile     = "file"
	SourceDetected = "detected"
)

const (
	gateFileName    = "gate.json"
	gateFileVersion = 1
)

// Check is one command of the gate.
type Check struct {
	Name      string   `json:"name"`
	Argv      []string `json:"argv"`
	Kind      string   `json:"kind"`
	Source    string   `json:"source"`
	Available bool     `json:"available"`
}

// Table is the gate: the checks, in the order they run, and where they came
// from.
type Table struct {
	Source string  `json:"source"`
	Checks []Check `json:"checks"`
}

// ErrGateConfig is wrapped by the error for a gate.json that cannot be used.
var ErrGateConfig = errors.New("invalid gate configuration")

// gateFile is gate.json. A check's availability is the machine's and is
// worked out on every read, so it is accepted here and never kept.
type gateFile struct {
	Version int         `json:"version"`
	Checks  []fileCheck `json:"checks"`
}

type fileCheck struct {
	Name      string          `json:"name,omitempty"`
	Argv      []string        `json:"argv"`
	Kind      string          `json:"kind,omitempty"`
	Source    string          `json:"source,omitempty"`
	Available json.RawMessage `json:"available,omitempty"`
}

// GatePath is where dir keeps its gate.json.
func GatePath(dir string) string {
	return filepath.Join(dir, agentdir.Name, gateFileName)
}

// readGateFile reads <dir>/.agent-fox/gate.json. found is false when there
// is none; a file that cannot be used is an error wrapping ErrGateConfig that
// names it.
func readGateFile(dir string) (checks []Check, found bool, err error) {
	path := GatePath(dir)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("%w: %s: %v", ErrGateConfig, path, err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file gateFile
	if err := dec.Decode(&file); err != nil {
		return nil, true, fmt.Errorf("%w: %s: %v", ErrGateConfig, path, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, true, fmt.Errorf("%w: %s: unexpected content after the document", ErrGateConfig, path)
	}
	if file.Version != gateFileVersion {
		return nil, true, fmt.Errorf("%w: %s: version must be %d, not %d", ErrGateConfig, path, gateFileVersion, file.Version)
	}

	checks, err = fromFile(dir, file.Checks)
	if err != nil {
		return nil, true, fmt.Errorf("%w: %s: %v", ErrGateConfig, path, err)
	}
	return checks, true, nil
}

// fromFile turns the checks of a gate.json into checks, filling in what the
// file leaves out and refusing what cannot run.
func fromFile(dir string, in []fileCheck) ([]Check, error) {
	checks := make([]Check, 0, len(in))
	seen := map[string]bool{}
	for i, c := range in {
		if len(c.Argv) == 0 || c.Argv[0] == "" {
			return nil, fmt.Errorf("check %d has no argv", i+1)
		}
		if c.Name == "" {
			c.Name = strings.Join(c.Argv, " ")
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("two checks are named %q", c.Name)
		}
		seen[c.Name] = true
		switch c.Kind {
		case "":
			c.Kind = KindCheck
		case KindLint, KindTest, KindCheck:
		default:
			return nil, fmt.Errorf("check %q has the kind %q, not lint, test or check", c.Name, c.Kind)
		}
		if c.Source == "" {
			c.Source = gateFileName
		}
		checks = append(checks, Check{
			Name:      c.Name,
			Argv:      c.Argv,
			Kind:      c.Kind,
			Source:    c.Source,
			Available: available(dir, c.Argv[0]),
		})
	}
	return checks, nil
}

// WriteGateFile writes checks to <dir>/.agent-fox/gate.json, through a
// temporary file in the same directory and a rename, so that a reader sees
// the old file or the new one and never half of one.
func WriteGateFile(dir string, checks []Check) error {
	agent, err := agentdir.Ensure(dir)
	if err != nil {
		return err
	}
	file := gateFile{Version: gateFileVersion, Checks: make([]fileCheck, len(checks))}
	for i, c := range checks {
		file.Checks[i] = fileCheck{Name: c.Name, Argv: slices.Clone(c.Argv), Kind: c.Kind, Source: c.Source}
	}
	var doc bytes.Buffer
	enc := json.NewEncoder(&doc)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(file); err != nil {
		return err
	}

	return agentdir.WriteFileAtomic(filepath.Join(agent, gateFileName), doc.Bytes())
}

// available reports whether program can be run from dir: a name is looked up
// on PATH, a path is looked for as it is, relative to dir. It is the one thing
// in this package that depends on the machine, and nothing else reads it.
func available(dir, program string) bool {
	if !strings.ContainsRune(program, '/') && !strings.ContainsRune(program, filepath.Separator) {
		_, err := exec.LookPath(program)
		return err == nil
	}
	path := program
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}
