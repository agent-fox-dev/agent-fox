package triage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
)

// FormatVersion is the version of the format of triage.json.
const FormatVersion = 1

// Fingerprint identifies what was triaged, so that a resume can find the run it
// resumes: the issue ref for an issue, else the hash of the input's body.
func Fingerprint(r Report) string {
	if r.Kind == KindIssue {
		return r.Ref.String()
	}
	sum := sha256.Sum256([]byte(r.Body))
	return hex.EncodeToString(sum[:])
}

// WriteRunFiles writes brief.md, original.md and triage.json into the run
// directory, each atomically, and returns the facts as triage.json holds them:
// the format version, the hash of the original report, and the length and hash
// of the prefix of the brief, filled in from the brief.
func WriteRunFiles(runDir string, b Brief, facts Facts) (Facts, error) {
	prefix := sha256.Sum256([]byte(b.Prefix))
	original := sha256.Sum256([]byte(b.Original))
	facts.FormatVersion = FormatVersion
	facts.OriginalReportSHA256 = hex.EncodeToString(original[:])
	facts.Prefix = PrefixFact{Bytes: len(b.Prefix), SHA256: hex.EncodeToString(prefix[:])}

	data, err := json.MarshalIndent(facts, "", "  ")
	if err != nil {
		return facts, err
	}
	for _, f := range []struct {
		name    string
		content []byte
	}{
		{"brief.md", []byte(b.Document)},
		{"original.md", []byte(b.Original)},
		{"triage.json", append(data, '\n')},
	} {
		if err := agentdir.WriteFileAtomic(filepath.Join(runDir, f.name), f.content); err != nil {
			return facts, fmt.Errorf("cannot write %s in the run directory: %w", f.name, err)
		}
	}
	return facts, nil
}
