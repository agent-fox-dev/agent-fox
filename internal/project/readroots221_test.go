package project

import (
	"os"
	"path/filepath"
	"testing"
)

// Issue #221: a local dependency outside the repository is a read root
// whatever ecosystem declares it: npm's file:/link:, Cargo's path, pip's -e.
func TestReadRootsFromEveryEcosystem(t *testing.T) {
	parent := t.TempDir()
	for _, d := range []string{"repo", "shared", "lib", "pylib"} {
		if err := os.MkdirAll(filepath.Join(parent, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(parent, "repo")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"dependencies": {"@acme/shared": "file:../shared", "left-pad": "^1.0.0"}}`)
	write("Cargo.toml", "[package]\nname = \"x\"\n\n[dependencies]\nacme-lib = { path = \"../lib\" }\nserde = \"1\"\n")
	write("requirements.txt", "requests==2.0\n-e ../pylib\n")
	got := map[string]string{}
	for _, r := range ReadRoots(root) {
		got[r.Path] = r.Module + " via " + r.Source
	}
	for path, want := range map[string]string{
		"../shared": "@acme/shared via package.json",
		"../lib":    "acme-lib via Cargo.toml",
		"../pylib":  "../pylib via requirements.txt",
	} {
		if got[path] != want {
			t.Errorf("read root %s = %q, want %q (all: %v)", path, got[path], want, got)
		}
	}
}
