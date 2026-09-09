package afspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidTransition(t *testing.T) {
	allowed := []struct{ from, to string }{
		{"draft", "active"},
		{"active", "sealed"},
		{"sealed", "superseded"},
		{"sealed", "archived"},
	}
	for _, tc := range allowed {
		if !ValidTransition(tc.from, tc.to) {
			t.Errorf("%s → %s should be allowed", tc.from, tc.to)
		}
	}
	rejected := []struct{ from, to string }{
		{"draft", "sealed"},
		{"active", "draft"},
		{"archived", "active"},
		{"draft", "nonsense"},
	}
	for _, tc := range rejected {
		if ValidTransition(tc.from, tc.to) {
			t.Errorf("%s → %s should be rejected", tc.from, tc.to)
		}
	}
}

func TestTransitionToActiveHashesTheIntent(t *testing.T) {
	dir := copyFixture(t, fixtureValidSpec)
	spec := loadFixture(t, dir)

	activated, err := spec.Transition("active", dir)
	if err != nil {
		t.Fatalf("Transition = %v", err)
	}
	if activated.Status != "active" {
		t.Errorf("status = %q; want active", activated.Status)
	}
	if activated.IntentHash == nil || *activated.IntentHash == "" {
		t.Fatal("the intent hash was not computed at draft → active")
	}
	if spec.Status != "draft" || spec.IntentHash != nil {
		t.Error("Transition mutated the receiver")
	}

	want, err := ComputeIntentHash(spec.PRDBody)
	if err != nil {
		t.Fatal(err)
	}
	if *activated.IntentHash != want {
		t.Errorf("hash = %q; want %q", *activated.IntentHash, want)
	}

	reloaded := loadFixture(t, dir)
	if reloaded.Status != "active" {
		t.Error("the transition was not persisted")
	}
}

func TestTransitionRejectsIllegalTargets(t *testing.T) {
	dir := copyFixture(t, fixtureValidSpec)
	spec := loadFixture(t, dir)
	if _, err := spec.Transition("sealed", dir); err == nil {
		t.Fatal("draft → sealed was accepted")
	}
	if loadFixture(t, dir).Status != "draft" {
		t.Error("a rejected transition still wrote to disk")
	}
}

func TestSupersedeAddsADeprecationBanner(t *testing.T) {
	dir := copyFixture(t, fixtureValidSpec)
	spec := loadFixture(t, dir)

	active, err := spec.Transition("active", dir)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := active.Transition("sealed", dir)
	if err != nil {
		t.Fatal(err)
	}

	superseded, err := sealed.Supersede("02", dir)
	if err != nil {
		t.Fatalf("Supersede = %v", err)
	}
	if superseded.Status != "superseded" {
		t.Errorf("status = %q; want superseded", superseded.Status)
	}
	if !strings.Contains(superseded.PRDBody, "02") {
		t.Error("the deprecation banner does not name the superseding spec")
	}
}

func TestMoveToArchive(t *testing.T) {
	root := t.TempDir()
	specDir := filepath.Join(root, "01_test_feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := copyFixture(t, fixtureValidSpec)
	entries, _ := os.ReadDir(src)
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(src, e.Name()))
		if err := os.WriteFile(filepath.Join(specDir, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	spec := loadFixture(t, specDir)
	active, err := spec.Transition("active", specDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := active.Transition("sealed", specDir); err != nil {
		t.Fatal(err)
	}

	if err := MoveToArchive(specDir, root); err != nil {
		t.Fatalf("MoveToArchive = %v", err)
	}
	if _, err := os.Stat(specDir); !os.IsNotExist(err) {
		t.Error("the spec directory is still in place after archiving")
	}
	archived := filepath.Join(root, "archive", "01_test_feature")
	if _, err := os.Stat(archived); err != nil {
		t.Fatalf("the spec is not under archive/: %v", err)
	}
	if loadFixture(t, archived).Status != "archived" {
		t.Error("the archived spec is not in the archived state")
	}
}

func TestComputeIntentHash(t *testing.T) {
	body := "# Title\n\n## Intent\n\nThe intent text.\n\n## Goals\n\n- something\n"
	first, err := ComputeIntentHash(body)
	if err != nil {
		t.Fatalf("ComputeIntentHash = %v", err)
	}
	if len(first) != 64 {
		t.Errorf("hash length = %d; want a 64-character SHA-256 hex digest", len(first))
	}

	// Editing a section other than Intent must not change the hash.
	edited := strings.Replace(body, "- something", "- something else", 1)
	second, err := ComputeIntentHash(edited)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Error("editing a non-Intent section changed the intent hash")
	}

	// Editing Intent must change it.
	drifted, err := ComputeIntentHash(strings.Replace(body, "The intent text.", "Something else.", 1))
	if err != nil {
		t.Fatal(err)
	}
	if drifted == first {
		t.Error("editing the Intent section did not change the hash")
	}
}

func TestComputeIntentHashRequiresTheSection(t *testing.T) {
	_, err := ComputeIntentHash("# Title\n\nNo intent section here.\n")
	if err == nil {
		t.Fatal("a body with no ## Intent section produced a hash")
	}
}
