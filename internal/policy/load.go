package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/project"
)

// guardFileName is the policy a run keeps in .agent-fox.
const guardFileName = "guard.json"

// guardFileVersion is the version of the policy file this package reads.
const guardFileVersion = 1

// ConfigError says that a policy file cannot be used.
type ConfigError struct {
	Path    string
	Message string
}

func (e *ConfigError) Error() string { return e.Path + ": " + e.Message }

// policyFile is a policy file. A key that is not there is not the same as one
// that is empty: the first takes its default, the second is taken as it is.
type policyFile struct {
	Version        int         `json:"version"`
	Suite          *[][]string `json:"suite"`
	ProtectedPaths *[]string   `json:"protected_paths"`
	AllowPrograms  *[]string   `json:"allow_programs"`
}

// MarshalGuardFile is the content of a guard.json that holds the policy: every key is
// written, an empty list as an empty list, which LoadPolicy takes as it is.
func MarshalGuardFile(p Policy) ([]byte, error) {
	file := struct {
		Version        int        `json:"version"`
		Suite          [][]string `json:"suite"`
		ProtectedPaths []string   `json:"protected_paths"`
		AllowPrograms  []string   `json:"allow_programs"`
	}{guardFileVersion, p.Suite, p.ProtectedPaths, p.AllowPrograms}
	if file.Suite == nil {
		file.Suite = [][]string{}
	}
	if file.ProtectedPaths == nil {
		file.ProtectedPaths = []string{}
	}
	if file.AllowPrograms == nil {
		file.AllowPrograms = []string{}
	}
	var doc bytes.Buffer
	enc := json.NewEncoder(&doc)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(file); err != nil {
		return nil, err
	}
	return doc.Bytes(), nil
}

// LoadPolicy loads the policy for the checkout dir from the first source that
// exists: the file policyPath, which must be there; <dir>/.agent-fox/guard.json;
// or the defaults, with the suite that detection finds in dir. A key a file
// does not have takes its default. A file that cannot be used is a
// *ConfigError that names it, and is not half read.
func LoadPolicy(ctx context.Context, dir, policyPath string) (Policy, error) {
	path, explicit := policyPath, policyPath != ""
	if !explicit {
		path = filepath.Join(dir, agentdir.Name, guardFileName)
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return fromFile(ctx, dir, path, data)
	case !explicit && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)):
		return withDefaults(ctx, dir, policyFile{})
	}
	return Policy{}, &ConfigError{Path: path, Message: fmt.Sprintf("cannot be read: %v", err)}
}

// fromFile reads the policy file at path, which holds data.
func fromFile(ctx context.Context, dir, path string, data []byte) (Policy, error) {
	fail := func(format string, args ...any) (Policy, error) {
		return Policy{}, &ConfigError{Path: path, Message: fmt.Sprintf(format, args...)}
	}
	var f policyFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return fail("is not a policy: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fail("holds more than one document")
	}
	if f.Version != guardFileVersion {
		return fail("must have version %d, not %d", guardFileVersion, f.Version)
	}
	if f.Suite != nil {
		for i, form := range *f.Suite {
			if len(form) == 0 {
				return fail("suite[%d] is empty", i)
			}
		}
	}
	if f.ProtectedPaths != nil {
		for i, p := range *f.ProtectedPaths {
			if err := checkProtectedPath(p); err != nil {
				return fail("protected_paths[%d] %q %v", i, p, err)
			}
		}
	}
	if f.AllowPrograms != nil {
		for i, p := range *f.AllowPrograms {
			if p == "" {
				return fail("allow_programs[%d] is empty", i)
			}
		}
	}
	return withDefaults(ctx, dir, f)
}

// checkProtectedPath says why p cannot be a protected path: one is relative to
// the checkout and below it.
func checkProtectedPath(p string) error {
	clean := filepath.ToSlash(filepath.Clean(p))
	switch {
	case p == "":
		return errors.New("is empty")
	case filepath.IsAbs(p) || strings.HasPrefix(p, "/"):
		return errors.New("is not relative to the checkout")
	case clean == ".." || strings.HasPrefix(clean, "../") || clean == ".":
		return errors.New("is not below the checkout")
	}
	return nil
}

// withDefaults is the policy of a file, with the default for each key it does
// not have. Detection runs only if the suite is wanted.
func withDefaults(ctx context.Context, dir string, f policyFile) (Policy, error) {
	p := Policy{ProtectedPaths: DefaultProtectedPaths()}
	if f.ProtectedPaths != nil {
		p.ProtectedPaths = *f.ProtectedPaths
	}
	if f.AllowPrograms != nil {
		p.AllowPrograms = *f.AllowPrograms
	}
	if f.Suite != nil {
		p.Suite = *f.Suite
		return p, nil
	}
	files, err := checks.FileView(ctx, dir)
	if err != nil {
		return Policy{}, err
	}
	detection, err := project.Detect(dir, files)
	if errors.Is(err, project.ErrGateConfig) {
		path := project.GatePath(dir)
		return Policy{}, &ConfigError{Path: path, Message: strings.TrimPrefix(err.Error(), project.ErrGateConfig.Error()+": "+path+": ")}
	}
	if err != nil {
		return Policy{}, err
	}
	p.Suite = detection.Suite
	return p, nil
}
