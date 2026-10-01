package toolio

import (
	"bytes"
	"encoding/json"
	"flag"
)

// InputKinds is the closed list of kinds an input is classified as, in the
// order --schema reports them. It is the same for every tool: a tool that
// refuses one of them (impl refuses an issue) does so in its own CheckInput,
// not by classifying differently.
var InputKinds = []string{string(KindText), string(KindFile), string(KindStdin), string(KindIssue)}

// SelfDescription is the document --schema prints in place of running.
type SelfDescription struct {
	Tool          string            `json:"tool"`
	SchemaVersion string            `json:"schema_version"`
	Description   string            `json:"description"`
	Input         SelfDescribeInput `json:"input"`
	// Flags is a standalone JSON Schema document of the tool's flag set.
	Flags SchemaObject `json:"flags"`
	// Result is a standalone JSON Schema document of the envelope the tool
	// writes, with its own Result type in place of the envelope's result.
	Result SchemaObject `json:"result"`
	// ExitCodes holds only the codes this tool can return. encoding/json
	// renders the integer keys as strings, in sorted order.
	ExitCodes map[int]string `json:"exit_codes"`
}

// SelfDescribeInput is what the tool's one positional input may be.
type SelfDescribeInput struct {
	Description string   `json:"description"`
	Kinds       []string `json:"kinds"`
}

// describe assembles the self-description document from the App's own
// declarations and the fully-populated flag set Main built. It reads nothing
// else: no environment, no directory, no network.
func (a App) describe(fs *flag.FlagSet) SelfDescription {
	codes := a.ExitCodes
	if codes == nil {
		codes = map[int]string{}
	}
	return SelfDescription{
		Tool:          a.Name,
		SchemaVersion: SchemaVersion,
		Description:   a.Description,
		Input:         SelfDescribeInput{Description: a.InputDescription, Kinds: append([]string(nil), InputKinds...)},
		Flags:         BuildFlagsDocument(fs),
		Result:        BuildResultDocument(a.ResultSample),
		ExitCodes:     codes,
	}
}

// renderSelfDescription renders the document the way Emit renders the
// envelope: indented, followed by a newline.
func renderSelfDescription(d SelfDescription) ([]byte, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(bytes.TrimRight(b, "\n"), '\n'), nil
}
