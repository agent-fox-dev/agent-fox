package agentfox

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// extractConst evaluates a string constant from a Go source file. It handles
// raw string literals and interpreted string literals, and concatenation with +.
func extractConst(t *testing.T, file, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, n := range vs.Names {
				if n.Name == name && i < len(vs.Values) {
					return evalStringExpr(t, vs.Values[i])
				}
			}
		}
	}
	t.Fatalf("const %s not found in %s", name, file)
	return ""
}

func evalStringExpr(t *testing.T, expr ast.Expr) string {
	t.Helper()
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			s, err := strconv.Unquote(e.Value)
			if err != nil {
				t.Fatalf("unquote: %v", err)
			}
			return s
		}
		return e.Value
	case *ast.BinaryExpr:
		return evalStringExpr(t, e.X) + evalStringExpr(t, e.Y)
	}
	t.Fatalf("unsupported expr type %T", expr)
	return ""
}

// TS-17-33 (property): For each of the three system prompts that say callers,
// the find_references clause appears once at step 2, and removing it gives
// the base prompt byte for byte.
//
// Verifies: 17-REQ-3.1, 17-REQ-3.2
func TestTS17_33_FindReferencesClauseInCallerPrompts(t *testing.T) {
	// The clause pattern: an optional single space, then (`find_references`
	// up to the closing parenthesis.
	re := regexp.MustCompile(`(?s) ?\(` + "`" + `find_references` + "`" + `[^)]*\)`)

	cases := []struct {
		file     string
		name     string
		basePath string
	}{
		{"codefix/phases.go", "analysisSystemPrompt", "testdata/prompts/base/fix_analysis.txt"},
		{"codeimpl/prompts.go", "surveySystemPrompt", "testdata/prompts/base/impl_survey.txt"},
		{"issuetriage/triage.go", "systemPrompt", "testdata/prompts/base/triage_system.txt"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := extractConst(t, tc.file, tc.name)
			base, err := os.ReadFile(tc.basePath)
			if err != nil {
				t.Fatalf("read base: %v", err)
			}

			// Step 2 is from '\n2. ' to '\n3. '.
			s2start := strings.Index(p, "\n2. ")
			s3start := strings.Index(p, "\n3. ")
			if s2start < 0 || s3start < 0 || s3start <= s2start {
				t.Fatalf("cannot find step 2 boundaries in %s", tc.name)
			}
			step2 := p[s2start:s3start]

			// Step 2 contains exactly one clause.
			matches := re.FindAllString(step2, -1)
			if len(matches) != 1 {
				t.Errorf("step 2 has %d clause matches, want 1: %v", len(matches), matches)
			}

			// The clause names find_references and says who uses a declaration.
			if len(matches) > 0 {
				clause := matches[0]
				if !strings.Contains(clause, "find_references") {
					t.Errorf("clause does not name find_references: %q", clause)
				}
				if !strings.Contains(clause, "who uses a declaration") {
					t.Errorf("clause does not say 'who uses a declaration': %q", clause)
				}
			}

			// The clause sits in the same sentence as 'callers'.
			if !strings.Contains(step2, "callers") {
				t.Errorf("step 2 does not contain 'callers'")
			}

			// find_references appears exactly once in the whole prompt.
			if n := strings.Count(p, "find_references"); n != 1 {
				t.Errorf("find_references appears %d times in the whole prompt, want 1", n)
			}

			// Removing the clause gives the base text byte for byte.
			stripped := re.ReplaceAllString(p, "")
			if stripped != string(base) {
				t.Errorf("prompt with clause removed does not equal base text.\nGot length %d, want %d",
					len(stripped), len(base))
				// Show the first difference.
				for i := 0; i < len(stripped) && i < len(base); i++ {
					if stripped[i] != base[i] {
						t.Errorf("first difference at byte %d: got %q, want %q",
							i, stripped[max(0, i-20):min(len(stripped), i+20)],
							string(base[max(0, i-20):min(len(base), i+20)]))
						break
					}
				}
			}
		})
	}
}

// TS-17-34 (unit): Every other system prompt, every spec template and the
// repository map's golden files are byte-identical to the base commit.
//
// Verifies: 17-REQ-3.3
func TestTS17_34_OtherPromptsAndTemplatesUnchanged(t *testing.T) {
	// SHA-256 digests recorded from the base commit.
	constDigests := map[string]string{
		"codefix/implementSystemPrompt":  "2c752685236c58af2ddd8a61d3aa5ff812dc17380aaf80d9d7577badda402528",
		"codeimpl/implementSystemPrompt": "34b3258d779198ed4a26dc4110e129bfa6f63893c580cf30c2cd91539127ab49",
		"codeimpl/repairSystemPrompt":    "e7c23ada280c67edb896c56df06b72b616dee4e42ee25cfd1f4fce5ddcc6d4da",
		"codeimpl/resolveSystemPrompt":   "d40ac5035d300da8252e00cfae3a6a08aec55a3708ea7517ded68431d2984e04",
		"conform/ReviewSystemPrompt":     "b1cdd1f70fddc53dca5d5fe28de2feee59f20053b2d2c10c3a5371260a9d402e",
	}
	constSources := map[string][2]string{
		"codefix/implementSystemPrompt":  {"codefix/phases.go", "implementSystemPrompt"},
		"codeimpl/implementSystemPrompt": {"codeimpl/prompts.go", "implementSystemPrompt"},
		"codeimpl/repairSystemPrompt":    {"codeimpl/prompts.go", "repairSystemPrompt"},
		"codeimpl/resolveSystemPrompt":   {"codeimpl/prompts.go", "resolveSystemPrompt"},
		"conform/ReviewSystemPrompt":     {"internal/conform/prompt.go", "ReviewSystemPrompt"},
	}

	for id, want := range constDigests {
		src := constSources[id]
		text := extractConst(t, src[0], src[1])
		got := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
		if got != want {
			t.Errorf("%s: SHA-256 = %s, want %s", id, got, want)
		}
	}

	fileDigests := map[string]string{
		"specgen/templates/prd_system.md":                   "c449fee46efedfac9536704ccdcc75f3441e564de13b639901d4d4991e4911d1",
		"specgen/templates/prd_user.md":                     "270edc3ae1645d449306f02f1294188e910e01787f41f947c5952d6d41b68772",
		"specgen/templates/generation_system.md":            "733622c9feb233faf373a05b8eb4fd0b3abb242f01d866dfc346b899cb68b15c",
		"specgen/templates/generation_user_base.md":         "2b8f8cf93ed820e7ee0e283f8ea13d699e806da9baa1afeaa7b2abe73a8aba5d",
		"specgen/templates/generation_user_requirements.md": "66daa0447e754ecf40e0f7bb109f2cacf778ca01d330598cad954b80d14a4943",
		"specgen/templates/generation_user_tasks.md":        "db8a672a2441d24838e3de665610ab3501fe993cf00c405a6ab83a67f54ebf85",
		"specgen/templates/generation_user_test_spec.md":    "431200991267a774bce8e24b50c241f2ddb82c13c2720c3c809593def3a7cbd5",
		"specgen/templates/architecture_user.md":            "bd631e8dc370bf4bfcdb45ce9de7712f468cae5cc7b0df31a5b8eb24b64b0341",
		"internal/repomap/testdata/golden_300.txt":          "2060b23892aae715800c78bd5fa0975b80c33bacd7648b8d6b1d6aa24f2d1f96",
		"internal/repomap/testdata/golden_1000.txt":         "2d83975a79cd34bf6d8a4432de647e2e14344abe07cfbfe8c224705d7cf3e7db",
		"internal/repomap/testdata/golden_6000.txt":         "a16d9ebab63a7bf1c98a802e4c1095497d5692d8810d4c05b7edb47b0be510a8",
	}

	for path, want := range fileDigests {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		got := fmt.Sprintf("%x", sha256.Sum256(b))
		if got != want {
			t.Errorf("%s: SHA-256 = %s, want %s", path, got, want)
		}
	}

	// No specgen template, internal/conform/prompt.go or internal/repomap/repomap.go
	// contains the text find_references.
	noFindRefs := []string{
		"internal/conform/prompt.go",
		"internal/repomap/repomap.go",
	}
	templateDir := "specgen/templates"
	entries, err := os.ReadDir(templateDir)
	if err != nil {
		t.Fatalf("read templates dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			noFindRefs = append(noFindRefs, filepath.Join(templateDir, e.Name()))
		}
	}
	for _, path := range noFindRefs {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if strings.Contains(string(b), "find_references") {
			t.Errorf("%s contains find_references", path)
		}
	}

	// The repomap opening sentence still names file_outline and find_symbol.
	repomap, err := os.ReadFile("internal/repomap/repomap.go")
	if err != nil {
		t.Fatalf("read repomap.go: %v", err)
	}
	rmText := string(repomap)
	if !strings.Contains(rmText, "file_outline") {
		t.Error("repomap.go does not mention file_outline")
	}
	if !strings.Contains(rmText, "find_symbol") {
		t.Error("repomap.go does not mention find_symbol")
	}
}
