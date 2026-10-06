package agentfox

import (
	"os"
	"strings"
	"testing"
)

const (
	codesearchModule  = "github.com/agentfox/agentkit-go/codesearch"
	codesearchReplace = "../agentkit-go/codesearch"
)

// TS-16-26 (unit): go.mod contains the codesearch require and replace directives
// Verifies: 16-REQ-8.1, 16-REQ-8.2
func TestTS16_26_GoModCarriesCodesearchRequireAndReplace(t *testing.T) {
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	var requires, replaces bool
	block := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == ")":
			block = ""
			continue
		case strings.HasSuffix(line, "("):
			block = strings.TrimSpace(strings.TrimSuffix(line, "("))
			continue
		}
		stmt := line
		kind := block
		if f := strings.Fields(line); len(f) > 0 && (f[0] == "require" || f[0] == "replace") {
			kind = f[0]
			stmt = strings.TrimSpace(strings.TrimPrefix(line, f[0]))
		}
		f := strings.Fields(stmt)
		if len(f) == 0 {
			continue
		}
		switch kind {
		case "require":
			if f[0] == codesearchModule {
				requires = true
			}
		case "replace":
			if f[0] == codesearchModule && len(f) >= 3 && f[1] == "=>" && f[2] == codesearchReplace {
				replaces = true
			}
		}
	}
	if !requires {
		t.Errorf("go.mod has no require for %s", codesearchModule)
	}
	if !replaces {
		t.Errorf("go.mod has no replace %s => %s", codesearchModule, codesearchReplace)
	}
}
