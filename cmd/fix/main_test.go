package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

func TestNormalizeArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "no pull flag",
			in:   []string{"-repo", "owner/repo", "bug description"},
			want: []string{"-repo", "owner/repo", "bug description"},
		},
		{
			name: "pull with equal and branch",
			in:   []string{"--pull=main", "bug description"},
			want: []string{"--pull=main", "bug description"},
		},
		{
			name: "single argument bare pull (help/no input)",
			in:   []string{"-pull"},
			want: []string{"-pull"},
		},
		{
			name: "pull with one positional input",
			in:   []string{"-pull", "bug description"},
			want: []string{"-pull", "bug description"},
		},
		{
			name: "pull with one positional and other flags",
			in:   []string{"-repo", "owner/repo", "-pull", "bug description"},
			want: []string{"-repo", "owner/repo", "-pull", "bug description"},
		},
		{
			name: "pull with branch and one positional input",
			in:   []string{"-pull", "dev", "bug description"},
			want: []string{"-pull=dev", "bug description"},
		},
		{
			name: "double dash pull with branch and one positional input",
			in:   []string{"--pull", "dev", "bug description"},
			want: []string{"--pull=dev", "bug description"},
		},
		{
			name: "pull with branch and flag before input",
			in:   []string{"-pull", "dev", "--dry-run", "bug description"},
			want: []string{"-pull=dev", "--dry-run", "bug description"},
		},
		{
			name: "flags before pull with branch and input",
			in:   []string{"--dry-run", "-pull", "dev", "bug description"},
			want: []string{"--dry-run", "-pull=dev", "bug description"},
		},
		{
			name: "two pull occurrences each keep their own prefix",
			in:   []string{"-pull", "a", "--pull", "b", "bug description"},
			want: []string{"-pull=a", "--pull=b", "bug description"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeArgs(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("normalizeArgs(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPullFlagParsing(t *testing.T) {
	cases := []struct {
		name       string
		val        string
		wantSet    bool
		wantBranch string
		wantErr    bool
	}{
		{
			name:       "empty bool string",
			val:        "",
			wantSet:    true,
			wantBranch: "",
		},
		{
			name:       "true bool string",
			val:        "true",
			wantSet:    true,
			wantBranch: "",
		},
		{
			name:       "false bool string",
			val:        "false",
			wantSet:    false,
			wantBranch: "",
		},
		{
			name:       "branch string",
			val:        "main",
			wantSet:    true,
			wantBranch: "main",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var pf pullFlag
			err := pf.Set(tc.val)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Set(%q) err = %v, wantErr %v", tc.val, err, tc.wantErr)
			}
			if pf.set != tc.wantSet {
				t.Errorf("pf.set = %v, want %v", pf.set, tc.wantSet)
			}
			if pf.branch != tc.wantBranch {
				t.Errorf("pf.branch = %q, want %q", pf.branch, tc.wantBranch)
			}
		})
	}
}

func findWorkspaceRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find workspace root containing go.mod")
		}
		dir = parent
	}
}

var (
	testBinDir string
	testBins   = map[string]string{}
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cmd-test-bins-*")
	if err != nil {
		panic(err)
	}
	testBinDir = dir

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}

func getTestBin(t *testing.T, cmdPkg string) string {
	if bin, ok := testBins[cmdPkg]; ok {
		return bin
	}
	root := findWorkspaceRoot(t)
	bin := filepath.Join(testBinDir, cmdPkg)
	buildCmd := exec.Command("go", "build", "-o", bin, "./cmd/"+cmdPkg)
	buildCmd.Dir = root
	out, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./cmd/%s failed: %v\n%s", cmdPkg, err, out)
	}
	testBins[cmdPkg] = bin
	return bin
}

func runCommand(t *testing.T, cmdPkg string, args ...string) (int, string) {
	bin := getTestBin(t, cmdPkg)
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	combined := stdout.String() + "\n" + stderr.String()
	if err == nil {
		return toolio.ExitOK, combined
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), combined
	}
	t.Fatalf("failed to run %s: %v\noutput: %s", cmdPkg, err, combined)
	return -1, combined
}

// extractUsageAndForgeField parses the main.go file for a cmd tool and extracts:
// - the value of `const usage`
// - whether Options binds Forge: d.Forge
// - whether it uses ghapi
func extractUsageAndForgeField(t *testing.T, cmdPkg string) (usageStr string, bindsForge bool, usesGhapi bool) {
	root := findWorkspaceRoot(t)
	filePath := filepath.Join(root, "cmd", cmdPkg, "main.go")
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parser.ParseFile(%s): %v", filePath, err)
	}

	for _, imp := range node.Imports {
		if strings.Contains(imp.Path.Value, "ghapi") {
			usesGhapi = true
		}
	}

	ast.Inspect(node, func(n ast.Node) bool {
		// Look for const usage = ...
		if genDecl, ok := n.(*ast.GenDecl); ok && genDecl.Tok == token.CONST {
			for _, spec := range genDecl.Specs {
				if vSpec, ok := spec.(*ast.ValueSpec); ok {
					for i, name := range vSpec.Names {
						if name.Name == "usage" && i < len(vSpec.Values) {
							if lit, ok := vSpec.Values[i].(*ast.BasicLit); ok {
								usageStr = strings.Trim(lit.Value, "`\"")
							}
						}
					}
				}
			}
		}

		// Look for Forge: d.Forge in composite literals
		if kv, ok := n.(*ast.KeyValueExpr); ok {
			keyIdent, kOk := kv.Key.(*ast.Ident)
			valSel, vOk := kv.Value.(*ast.SelectorExpr)
			if kOk && vOk && keyIdent.Name == "Forge" {
				if selX, ok := valSel.X.(*ast.Ident); ok && selX.Name == "d" && valSel.Sel.Name == "Forge" {
					bindsForge = true
				}
			}
		}

		return true
	})

	return usageStr, bindsForge, usesGhapi
}

// TS-04-32 (integration): CLI commands parse repository flag via issuex.ParseRepo and forward Forge client
func TestCLICommandsParseRepoAndForwardForge_TS_04_32(t *testing.T) {
	// 1. Verify issuex.ParseRepo parses multi-segment repository paths for --repo flags
	repo := "gitlab-org/subgroup/project"
	parsed, ok := issuex.ParseRepo(repo)
	if !ok || parsed.Owner != "gitlab-org/subgroup" || parsed.Name != "project" {
		t.Fatalf("issuex.ParseRepo(%q) = (%+v, %v), want Owner=%q, Name=%q, ok=true", repo, parsed, ok, "gitlab-org/subgroup", "project")
	}

	// Verify valid multi-segment repository is accepted by PreCheck for fix, impl, issue (not rejected with ExitUsage)
	for _, cmdName := range []string{"fix", "impl", "issue"} {
		code, out := runCommand(t, cmdName, "--repo", repo, "test input")
		if code == toolio.ExitUsage && strings.Contains(out, "cannot be parsed") {
			t.Errorf("cmd/%s unexpectedly rejected valid multi-segment repo %q: %s", cmdName, repo, out)
		}
	}

	commands := []string{"fix", "impl", "issue", "spec"}
	for _, cmdName := range commands {
		t.Run(cmdName, func(t *testing.T) {
			usage, bindsForge, usesGhapi := extractUsageAndForgeField(t, cmdName)

			// verify each command binds d.Forge to pipeline Options:
			if !bindsForge {
				t.Errorf("cmd/%s does not bind Forge: d.Forge in pipeline options", cmdName)
			}
			if usesGhapi {
				t.Errorf("cmd/%s still imports internal/ghapi", cmdName)
			}

			// verify usage text mentions GitHub and GitLab
			if !strings.Contains(usage, "GitHub") {
				t.Errorf("cmd/%s usage text does not mention GitHub", cmdName)
			}
			if !strings.Contains(usage, "GitLab") {
				t.Errorf("cmd/%s usage text does not mention GitLab", cmdName)
			}
		})
	}
}

// TS-04-33 (unit): CLI commands reject malformed --repo flag with usage error
func TestCLICommandsRejectMalformedRepo_TS_04_33(t *testing.T) {
	const (
		cmdFix   = "fix"
		cmdImpl  = "impl"
		cmdIssue = "issue"
	)

	badRepos := []string{"single", "trailing/", "///", "owner//repo"}
	for _, badRepo := range badRepos {
		t.Run(badRepo, func(t *testing.T) {
			codeFix, outFix := runCommand(t, cmdFix, "--repo", badRepo, "arg")
			if codeFix != toolio.ExitUsage {
				t.Errorf("cmdFix with --repo %q exit code = %d, want %d (ExitUsage)", badRepo, codeFix, toolio.ExitUsage)
			}
			if !strings.Contains(outFix, "cannot be parsed") {
				t.Errorf("cmdFix with --repo %q output does not indicate cannot be parsed: %s", badRepo, outFix)
			}

			codeImpl, outImpl := runCommand(t, cmdImpl, "--repo", badRepo, "arg")
			if codeImpl != toolio.ExitUsage {
				t.Errorf("cmdImpl with --repo %q exit code = %d, want %d (ExitUsage)", badRepo, codeImpl, toolio.ExitUsage)
			}
			if !strings.Contains(outImpl, "cannot be parsed") {
				t.Errorf("cmdImpl with --repo %q output does not indicate cannot be parsed: %s", badRepo, outImpl)
			}

			codeIssue, outIssue := runCommand(t, cmdIssue, "--repo", badRepo, "arg")
			if codeIssue != toolio.ExitUsage {
				t.Errorf("cmdIssue with --repo %q exit code = %d, want %d (ExitUsage)", badRepo, codeIssue, toolio.ExitUsage)
			}
			if !strings.Contains(outIssue, "cannot be parsed") {
				t.Errorf("cmdIssue with --repo %q output does not indicate cannot be parsed: %s", badRepo, outIssue)
			}
		})
	}
}
