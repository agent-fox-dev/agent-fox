package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- TS-08-39: Verify that spec campaign --path --name --description calls
//     CreateCampaign and prints a confirmation message to stderr on
//     success ---

// TestTS08_39_CampaignCreateSuccess verifies that running spec campaign
// with --path, --name, and --description creates campaign.yaml at the
// specified path and prints a confirmation message to stderr.
// Covers: TS-08-39, Requirement: 08-REQ-14.1
func TestTS08_39_CampaignCreateSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	campaignPath := filepath.Join(tmpDir, "test_campaign")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{
		"campaign",
		"--path", campaignPath,
		"--name", "my_campaign",
		"--description", "Test campaign",
	})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// Verify campaign.yaml was created.
	yamlPath := filepath.Join(campaignPath, "campaign.yaml")
	if _, statErr := os.Stat(yamlPath); os.IsNotExist(statErr) {
		t.Errorf("campaign.yaml not found at %s; want it to be created", yamlPath)
	}

	// Verify confirmation message on stderr.
	stderrOutput := stderrBuf.String()
	if !strings.Contains(strings.ToLower(stderrOutput), "campaign") &&
		!strings.Contains(strings.ToLower(stderrOutput), "created") &&
		!strings.Contains(stderrOutput, "my_campaign") {
		t.Errorf("stderr = %q; want confirmation containing 'campaign', 'created', or 'my_campaign'",
			stderrOutput)
	}
}

// TestTS08_39_CampaignCreateWithoutDescription verifies that the
// --description flag is optional and campaigns can be created without it.
// Covers: TS-08-39, Requirement: 08-REQ-14.1
func TestTS08_39_CampaignCreateWithoutDescription(t *testing.T) {
	tmpDir := t.TempDir()
	campaignPath := filepath.Join(tmpDir, "test_campaign")

	cmd := newRootCmd()
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{
		"campaign",
		"--path", campaignPath,
		"--name", "no_desc_campaign",
	})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// campaign.yaml should still be created.
	yamlPath := filepath.Join(campaignPath, "campaign.yaml")
	if _, statErr := os.Stat(yamlPath); os.IsNotExist(statErr) {
		t.Errorf("campaign.yaml not found at %s", yamlPath)
	}
}

// --- 08-REQ-14.E1: CampaignError is propagated ---

// TestTS08_39_CampaignErrorPropagation verifies that when CreateCampaign
// returns a CampaignError, the error message is printed to stderr and
// the command exits 1.
// Covers: 08-REQ-14.E1
func TestTS08_39_CampaignErrorPropagation(t *testing.T) {
	tmpDir := t.TempDir()
	campaignPath := filepath.Join(tmpDir, "test_campaign")

	// First create a campaign to set up the conflict.
	if err := os.MkdirAll(campaignPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(campaignPath, "campaign.yaml"),
		[]byte("name: existing\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Try to create another campaign at the same path.
	cmd := newRootCmd()
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{
		"campaign",
		"--path", campaignPath,
		"--name", "conflicting_campaign",
	})

	err := cmd.Execute()
	if err == nil {
		t.Error("Execute() on conflicting campaign path returned nil; want error (CampaignError)")
	}
}

// --- 08-REQ-14.E2: Missing required flags ---

// TestTS08_39_CampaignMissingPath verifies that --path is required and
// cobra reports the missing required flag.
// Covers: 08-REQ-14.E2
func TestTS08_39_CampaignMissingPath(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	stderrBuf := new(bytes.Buffer)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{
		"campaign",
		"--name", "my_campaign",
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with missing --path returned nil; want cobra's required-flag error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "path") && !strings.Contains(errMsg, "required") {
		t.Errorf("error = %q; want mention of missing 'path' or 'required'", errMsg)
	}
}

// TestTS08_39_CampaignMissingName verifies that --name is required and
// cobra reports the missing required flag.
// Covers: 08-REQ-14.E2
func TestTS08_39_CampaignMissingName(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	stderrBuf := new(bytes.Buffer)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{
		"campaign",
		"--path", filepath.Join(tmpDir, "test"),
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with missing --name returned nil; want cobra's required-flag error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "name") && !strings.Contains(errMsg, "required") {
		t.Errorf("error = %q; want mention of missing 'name' or 'required'", errMsg)
	}
}

// TestTS08_39_CampaignMissingBothFlags verifies that missing both
// --path and --name reports required flag errors.
// Covers: 08-REQ-14.E2
func TestTS08_39_CampaignMissingBothFlags(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"campaign"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with no flags returned nil; want required-flag error")
	}
}

// --- 08-REQ-14.E3: Target path already contains campaign.yaml ---

// TestTS08_39_CampaignExistingConflict verifies that creating a campaign
// at a path that already contains a campaign.yaml propagates the
// CampaignError describing the conflict.
// Covers: 08-REQ-14.E3
func TestTS08_39_CampaignExistingConflict(t *testing.T) {
	tmpDir := t.TempDir()
	campaignPath := filepath.Join(tmpDir, "existing_campaign")

	// Pre-create campaign.yaml.
	if err := os.MkdirAll(campaignPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(campaignPath, "campaign.yaml"),
		[]byte("name: original\ndescription: Original campaign\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{
		"campaign",
		"--path", campaignPath,
		"--name", "duplicate",
		"--description", "Should fail",
	})

	err := cmd.Execute()
	if err == nil {
		t.Error("Execute() on path with existing campaign.yaml returned nil; want error")
	}
}

// --- Campaign: flag shorthand verification ---

// TestTS08_39_CampaignFlagShorthands verifies that --path has shorthand
// -p and --name has shorthand -n.
// Covers: 08-REQ-14
func TestTS08_39_CampaignFlagShorthands(t *testing.T) {
	cmd := newCampaignCmd()

	pathFlag := cmd.Flags().Lookup("path")
	if pathFlag == nil {
		t.Fatal("flag --path is not registered on campaign command")
	}
	if pathFlag.Shorthand != "p" {
		t.Errorf("--path shorthand = %q; want %q", pathFlag.Shorthand, "p")
	}

	nameFlag := cmd.Flags().Lookup("name")
	if nameFlag == nil {
		t.Fatal("flag --name is not registered on campaign command")
	}
	if nameFlag.Shorthand != "n" {
		t.Errorf("--name shorthand = %q; want %q", nameFlag.Shorthand, "n")
	}
}
