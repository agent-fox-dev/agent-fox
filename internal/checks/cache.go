package checks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/project"
)

// gateDirName is the directory of .agent-fox that holds the gate runs.
const gateDirName = "gate"

// resultFile is the stored result of a gate run, in the directory of its id.
const resultFile = "result.json"

// mode is how the checks of a gate run are run, which the key holds: in the
// reduced environment of gitx.ReducedEnvRunner, the one way there is.
const mode = "reduced"

// Key identifies a gate run: what was measured, and by which checks.
type Key struct {
	// Sum is the SHA-256 of the key, in hex.
	Sum string
	// ID is the first 12 characters of Sum: the name of the result.
	ID string
}

// KeyOf is the key of running checks on the tree with the hash tree: the
// SHA-256 of the tree hash, the canonical JSON of the checks and the mode. The
// canonical form holds what a check is (name, argv and kind), with the keys
// in order, and leaves out what is about the machine (availability) or about
// where the check came from (source), so that the same table is the same key
// on every machine.
func KeyOf(tree string, checks []project.Check) Key {
	sum := sha256.Sum256([]byte(tree + string(canonicalTable(checks)) + mode))
	hexSum := hex.EncodeToString(sum[:])
	return Key{Sum: hexSum, ID: hexSum[:12]}
}

// canonicalTable is the JSON of the checks with every object's keys in order
// and without whitespace.
func canonicalTable(checks []project.Check) []byte {
	list := make([]map[string]any, len(checks))
	for i, c := range checks {
		list[i] = map[string]any{"name": c.Name, "argv": c.Argv, "kind": c.Kind}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(list) // strings, and lists of strings: nothing here can fail
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// Dir is the directory that keeps the logs and the result of the key.
func (k Key) Dir(dir string) string {
	return filepath.Join(dir, agentdir.Name, gateDirName, k.ID)
}

// relDir is Dir relative to the checkout, slash-separated, as a result names
// its logs.
func (k Key) relDir() string {
	return agentdir.Name + "/" + gateDirName + "/" + k.ID
}

// loadResult reads the result stored for the key. ok is false when there is
// none, or when what is there is no result of this key: a miss, which the
// next run replaces.
func loadResult(dir string, k Key) (res Result, ok bool) {
	data, err := os.ReadFile(filepath.Join(k.Dir(dir), resultFile))
	if err != nil || json.Unmarshal(data, &res) != nil {
		return Result{}, false
	}
	if res.ID != k.ID || res.Tree == "" {
		return Result{}, false
	}
	if res.Checks == nil {
		res.Checks = []CheckResult{}
	}
	if res.Skipped == nil {
		res.Skipped = []Skipped{}
	}
	return res, true
}

// storeResult writes the result of a run that completed, after its logs, so
// that a result is never there before the logs it names.
func storeResult(dir string, k Key, res Result) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		return err
	}
	return agentdir.WriteFileAtomic(filepath.Join(k.Dir(dir), resultFile), buf.Bytes())
}
