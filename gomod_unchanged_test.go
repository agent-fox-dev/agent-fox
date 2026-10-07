package agentfox

import (
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// TS-17-36 (unit): go.mod keeps the module requirements and replace
// directives it had at the base commit and go.sum is unchanged.
//
// Verifies: 17-REQ-2.8
func TestTS17_36_GoModAndGoSumUnchanged(t *testing.T) {
	// The expected require pairs, sorted by module path.
	type modVer struct{ mod, ver string }
	baseRequires := []modVer{
		{"cloud.google.com/go/compute/metadata", "v0.9.0"},
		{"github.com/RoaringBitmap/roaring/v2", "v2.19.0"},
		{"github.com/agentfox/agentkit-go", "v0.0.0"},
		{"github.com/agentfox/agentkit-go/codesearch", "v0.0.0"},
		{"github.com/beorn7/perks", "v1.0.1"},
		{"github.com/bits-and-blooms/bitset", "v1.24.4"},
		{"github.com/bmatcuk/doublestar/v4", "v4.10.0"},
		{"github.com/cespare/xxhash/v2", "v2.3.0"},
		{"github.com/cockroachdb/errors", "v1.11.3"},
		{"github.com/cockroachdb/logtags", "v0.0.0-20241215232642-bb51bb14a506"},
		{"github.com/cockroachdb/redact", "v1.1.5"},
		{"github.com/dustin/go-humanize", "v1.0.1"},
		{"github.com/fatih/color", "v1.18.0"},
		{"github.com/fsnotify/fsnotify", "v1.8.0"},
		{"github.com/getsentry/sentry-go", "v0.31.1"},
		{"github.com/go-enry/go-enry/v2", "v2.9.6"},
		{"github.com/go-enry/go-oniguruma", "v1.2.1"},
		{"github.com/goccy/go-yaml", "v1.19.2"},
		{"github.com/gogo/protobuf", "v1.3.2"},
		{"github.com/google/go-cmp", "v0.7.0"},
		{"github.com/google/uuid", "v1.6.0"},
		{"github.com/grafana/regexp", "v0.0.0-20240607082908-2cb410fa05da"},
		{"github.com/grpc-ecosystem/go-grpc-middleware/v2", "v2.3.3"},
		{"github.com/kr/pretty", "v0.3.1"},
		{"github.com/kr/text", "v0.2.0"},
		{"github.com/mattn/go-colorable", "v0.1.14"},
		{"github.com/mattn/go-isatty", "v0.0.20"},
		{"github.com/mschoch/smat", "v0.2.0"},
		{"github.com/munnerz/goautoneg", "v0.0.0-20191010083416-a7dc8b61c822"},
		{"github.com/opentracing/opentracing-go", "v1.2.0"},
		{"github.com/pkg/errors", "v0.9.1"},
		{"github.com/prometheus/client_golang", "v1.20.5"},
		{"github.com/prometheus/client_model", "v0.6.1"},
		{"github.com/prometheus/common", "v0.62.0"},
		{"github.com/prometheus/procfs", "v0.15.1"},
		{"github.com/rogpeppe/go-internal", "v1.14.1"},
		{"github.com/rs/xid", "v1.6.0"},
		{"github.com/santhosh-tekuri/jsonschema/v6", "v6.0.2"},
		{"github.com/sourcegraph/go-ctags", "v0.0.0-20250729094530-349a251d78d8"},
		{"github.com/sourcegraph/log", "v0.0.0-20241024013702-574f7079c888"},
		{"github.com/sourcegraph/zoekt", "v0.0.0-20260911061844-153817f643cd"},
		{"github.com/tetratelabs/wazero", "v1.9.0"},
		{"github.com/wasilibs/go-re2", "v1.10.0"},
		{"github.com/wasilibs/wazero-helpers", "v0.0.0-20240620070341-3dff1577cd52"},
		{"go.uber.org/atomic", "v1.11.0"},
		{"go.uber.org/multierr", "v1.11.0"},
		{"go.uber.org/zap", "v1.27.0"},
		{"golang.org/x/net", "v0.57.0"},
		{"golang.org/x/oauth2", "v0.37.0"},
		{"golang.org/x/sync", "v0.22.0"},
		{"golang.org/x/sys", "v0.47.0"},
		{"golang.org/x/text", "v0.40.0"},
		{"google.golang.org/genproto/googleapis/rpc", "v0.0.0-20260414002931-afd174a4e478"},
		{"google.golang.org/grpc", "v1.82.1"},
		{"google.golang.org/protobuf", "v1.36.11"},
	}

	baseReplaces := map[string]string{
		"github.com/agentfox/agentkit-go":            "../agentkit-go",
		"github.com/agentfox/agentkit-go/codesearch": "../agentkit-go/codesearch",
	}

	const baseGoSumDigest = "dd7cdba6b253199e2dff76eb43fe7154c6cf34af0f877721764ff32f72521ff1"

	// Parse go.mod with a simple line parser (same pattern as gomod_codesearch_test.go).
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}

	var requires []modVer
	replaces := map[string]string{}
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
			if len(f) >= 2 {
				requires = append(requires, modVer{f[0], f[1]})
			}
		case "replace":
			if len(f) >= 3 && f[1] == "=>" {
				replaces[f[0]] = f[2]
			}
		}
	}

	sort.Slice(requires, func(i, j int) bool { return requires[i].mod < requires[j].mod })

	// Assert require set equality.
	if len(requires) != len(baseRequires) {
		t.Errorf("go.mod has %d requires, want %d", len(requires), len(baseRequires))
	}
	for i := range baseRequires {
		if i >= len(requires) {
			break
		}
		if requires[i] != baseRequires[i] {
			t.Errorf("require[%d] = {%s, %s}, want {%s, %s}",
				i, requires[i].mod, requires[i].ver, baseRequires[i].mod, baseRequires[i].ver)
		}
	}

	// Assert replace directives.
	for mod, target := range baseReplaces {
		if got, ok := replaces[mod]; !ok {
			t.Errorf("go.mod has no replace for %s", mod)
		} else if got != target {
			t.Errorf("replace %s => %s, want %s", mod, got, target)
		}
	}
	if len(replaces) != len(baseReplaces) {
		t.Errorf("go.mod has %d replace directives, want %d", len(replaces), len(baseReplaces))
	}

	// Assert go.sum digest.
	sumRaw, err := os.ReadFile("go.sum")
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(sumRaw))
	if digest != baseGoSumDigest {
		t.Errorf("go.sum SHA-256 = %s, want %s", digest, baseGoSumDigest)
	}
}
