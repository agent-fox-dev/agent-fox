package conform

import (
	"os"
	"path/filepath"
	"testing"
)

// Issue #198: a doc source may cite a file under a read root — the local
// directory a go.mod replace names — and is checked against it like any
// other; outside the repository and the roots it is still refused.
func TestADocSourceMayCiteAReadRoot(t *testing.T) {
	parent := t.TempDir()
	repo, dep := filepath.Join(parent, "repo"), filepath.Join(parent, "agentkit-go")
	for _, d := range []string{repo, filepath.Join(dep, "tools"), filepath.Join(parent, "other")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	src := "package tools\n\ntype Index interface{ Invalidate(rel string) }\n"
	for _, p := range []string{filepath.Join(dep, "tools", "index.go"), filepath.Join(parent, "other", "index.go")} {
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ok := DocSource{Claim: "the index is invalidated by path", Source: "../agentkit-go/tools/index.go:3",
		Quote: "Invalidate(rel string)"}
	if err := VerifyDocSourceIn(repo, []string{dep}, ok); err != nil {
		t.Errorf("a source under a read root was refused: %v", err)
	}
	if err := VerifyDocSourceIn(repo, nil, ok); err == nil {
		t.Error("a source outside the repository was accepted with no read roots")
	}
	other := ok
	other.Source = "../other/index.go:3"
	if err := VerifyDocSourceIn(repo, []string{dep}, other); err == nil {
		t.Error("a source outside the repository and every read root was accepted")
	}
	wrong := ok
	wrong.Quote = "Invalidate(path string)"
	if err := VerifyDocSourceIn(repo, []string{dep}, wrong); err == nil {
		t.Error("a quote not on the read root's line was accepted")
	}
}
