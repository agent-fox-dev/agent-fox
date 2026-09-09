package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// --- TS-08-1: Verify global flags are registered with correct defaults
//     and that --version prints the embedded version string and exits 0 ---

// TestTS08_01_GlobalFlags_Defaults verifies that all global flags are
// registered on the root command with their documented default values.
// Covers: TS-08-1, Requirement: 08-REQ-1.1
func TestTS08_01_GlobalFlags_Defaults(t *testing.T) {
	cmd := newRootCmd()

	// --spec-dir / -d defaults to ".specs"
	specDirFlag := cmd.PersistentFlags().Lookup("spec-dir")
	if specDirFlag == nil {
		t.Fatal("flag --spec-dir is not registered on root command")
	}
	if specDirFlag.DefValue != ".specs" {
		t.Errorf("--spec-dir default = %q; want %q", specDirFlag.DefValue, ".specs")
	}
	if specDirFlag.Shorthand != "d" {
		t.Errorf("--spec-dir shorthand = %q; want %q", specDirFlag.Shorthand, "d")
	}

	// --source / -s defaults to "."
	sourceFlag := cmd.PersistentFlags().Lookup("source")
	if sourceFlag == nil {
		t.Fatal("flag --source is not registered on root command")
	}
	if sourceFlag.DefValue != "." {
		t.Errorf("--source default = %q; want %q", sourceFlag.DefValue, ".")
	}
	if sourceFlag.Shorthand != "s" {
		t.Errorf("--source shorthand = %q; want %q", sourceFlag.Shorthand, "s")
	}

	// --quiet / -q is registered
	quietFlag := cmd.PersistentFlags().Lookup("quiet")
	if quietFlag == nil {
		t.Fatal("flag --quiet is not registered on root command")
	}
	if quietFlag.Shorthand != "q" {
		t.Errorf("--quiet shorthand = %q; want %q", quietFlag.Shorthand, "q")
	}

	// --version is registered
	versionFlag := cmd.Flags().Lookup("version")
	if versionFlag == nil {
		t.Fatal("flag --version is not registered on root command")
	}
}

// TestTS08_01_VersionOutput verifies that running with --version prints
// the embedded version string and exits cleanly.
// Covers: TS-08-1, Requirement: 08-REQ-1.1
func TestTS08_01_VersionOutput(t *testing.T) {
	// Set a known version for testing.
	oldVersion := version
	version = "1.2.3"
	defer func() { version = oldVersion }()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--version"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "1.2.3") {
		t.Errorf("--version output = %q; want it to contain %q", output, "1.2.3")
	}
}

// TestTS08_01_SpecDirEnvVar verifies that SPEC_DIR environment variable
// overrides the --spec-dir default value.
// Covers: 08-REQ-1.E2
func TestTS08_01_SpecDirEnvVar(t *testing.T) {
	t.Setenv("SPEC_DIR", "/custom/specs")

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--help"}) // just run help to trigger flag parsing
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))

	_ = cmd.Execute()

	specDirFlag := cmd.PersistentFlags().Lookup("spec-dir")
	if specDirFlag == nil {
		t.Fatal("flag --spec-dir is not registered")
	}
	// After execution, the flag value should reflect the env var
	val := specDirFlag.Value.String()
	if val != "/custom/specs" {
		t.Errorf("--spec-dir with SPEC_DIR=/custom/specs = %q; want %q", val, "/custom/specs")
	}
}

// TestTS08_01_SpecDirEmptyEnvFallback verifies that an empty SPEC_DIR
// env var falls back to the default ".specs".
// Covers: 08-REQ-1.E1
func TestTS08_01_SpecDirEmptyEnvFallback(t *testing.T) {
	t.Setenv("SPEC_DIR", "")

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--help"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))

	_ = cmd.Execute()

	specDirFlag := cmd.PersistentFlags().Lookup("spec-dir")
	if specDirFlag == nil {
		t.Fatal("flag --spec-dir is not registered")
	}
	val := specDirFlag.Value.String()
	if val != ".specs" {
		t.Errorf("--spec-dir with empty SPEC_DIR = %q; want %q", val, ".specs")
	}
}

// TestTS08_01_SpecDirFlagOverridesEnv verifies that the --spec-dir flag
// takes precedence over the SPEC_DIR env var.
// Covers: 08-REQ-1.E2
func TestTS08_01_SpecDirFlagOverridesEnv(t *testing.T) {
	t.Setenv("SPEC_DIR", "/env/specs")

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--spec-dir", "/flag/specs", "--help"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))

	_ = cmd.Execute()

	specDirFlag := cmd.PersistentFlags().Lookup("spec-dir")
	if specDirFlag == nil {
		t.Fatal("flag --spec-dir is not registered")
	}
	val := specDirFlag.Value.String()
	if val != "/flag/specs" {
		t.Errorf("--spec-dir with flag and env set = %q; want %q", val, "/flag/specs")
	}
}

// --- TS-08-2: Verify that invoking without a subcommand displays help
//     and exits 0 ---

// TestTS08_02_NoSubcommandShowsHelp verifies that running the root
// command without a subcommand displays help text and exits cleanly.
// Covers: TS-08-2, Requirement: 08-REQ-1.2
func TestTS08_02_NoSubcommandShowsHelp(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Usage:") {
		t.Errorf("help output missing 'Usage:'; got %q", output)
	}
}

// --- TS-08-3: Verify banner is written to stderr (not stdout) ---

// TestTS08_03_BannerOnStderr verifies that when a subcommand executes
// without suppression conditions, the banner appears on stderr but
// not stdout.
// Covers: TS-08-3, Requirement: 08-REQ-1.3
func TestTS08_03_BannerOnStderr(t *testing.T) {
	// shouldShowBanner should return true when no suppression is active
	t.Setenv("AF_AGENT", "")
	show := shouldShowBanner(false, "new", []string{})
	if !show {
		t.Error("shouldShowBanner(quiet=false, subcmd='new', args=[]) = false; want true")
	}

	// Banner should be suppressed for validate/status/list
	for _, subcmd := range []string{"validate", "status", "list"} {
		if shouldShowBanner(false, subcmd, nil) {
			t.Errorf("shouldShowBanner(quiet=false, subcmd=%q) = true; want false", subcmd)
		}
	}

	// Banner should be suppressed with --quiet
	if shouldShowBanner(true, "new", nil) {
		t.Error("shouldShowBanner(quiet=true) = true; want false")
	}

	// Banner should be suppressed with --json in args
	if shouldShowBanner(false, "new", []string{"--json"}) {
		t.Error("shouldShowBanner(args=['--json']) = true; want false")
	}

	// printBanner should write to the given writer, not stdout.
	// The version is NOT embedded in the art itself (NS-REQ-3).
	// We set a sentinel version to verify it does not leak into the art.
	var bannerBuf bytes.Buffer
	oldVersion := version
	version = "sentinel-ver"
	defer func() { version = oldVersion }()
	printBanner(&bannerBuf)
	bannerOutput := bannerBuf.String()
	if len(bannerOutput) == 0 {
		t.Error("printBanner() produced no output; want ASCII art banner")
	}
	// Art must contain the fifth descender line from the Python SPEC_ART.
	if !strings.Contains(bannerOutput, "|_|") {
		t.Errorf("banner output = %q; want it to contain the descender line '|_|'", bannerOutput)
	}
	// Art must NOT use the old narrow-font glyphs.
	if strings.Contains(bannerOutput, "| _ |") || strings.Contains(bannerOutput, "|  _/") {
		t.Errorf("banner output = %q; want old narrow-font glyphs removed", bannerOutput)
	}
	// Version must NOT appear inside the art (it is printed separately by root.go).
	if strings.Contains(bannerOutput, "sentinel-ver") {
		t.Errorf("banner output = %q; want version string absent from art", bannerOutput)
	}
}

// TestTS08_03_BannerSuppressedAgentMode verifies that the banner is
// suppressed when AF_AGENT=1.
// Covers: TS-08-3, 08-PROP-6
func TestTS08_03_BannerSuppressedAgentMode(t *testing.T) {
	t.Setenv("AF_AGENT", "1")
	if shouldShowBanner(false, "new", nil) {
		t.Error("shouldShowBanner with AF_AGENT=1 = true; want false")
	}
}

// --- TS-08-4: Verify --source rejects non-existent paths ---

// TestTS08_04_SourceNonExistentPath verifies that passing a non-existent
// path to --source causes an error.
// Covers: TS-08-4, Requirement: 08-REQ-1.4
func TestTS08_04_SourceNonExistentPath(t *testing.T) {
	cmd := newRootCmd()
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--source", "/nonexistent/path/xyz", "--help"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with --source=/nonexistent/path/xyz returned nil error; want error")
	}

	errMsg := err.Error() + stderrBuf.String()
	if !strings.Contains(errMsg, "/nonexistent/path/xyz") {
		t.Errorf("error message = %q; want it to reference the invalid path", errMsg)
	}
}

// TestTS08_04_SourceExistingDirectory verifies that --source accepts
// an existing directory without error.
// Covers: TS-08-4, Requirement: 08-REQ-1.4
func TestTS08_04_SourceExistingDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--source", tmpDir, "--help"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() with --source=%s returned error: %v; want nil", tmpDir, err)
	}
}

// TestTS08_04_SourceFileNotDirectory verifies that --source rejects a
// path that exists but is a file (not a directory).
// Covers: 08-REQ-1.4
func TestTS08_04_SourceFileNotDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := tmpDir + "/afile.txt"
	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--source", filePath, "--help"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with --source pointing to a file returned nil; want error")
	}
}

// --- 08-REQ-1.E3: Unknown flag error ---

// TestTS08_01_UnknownFlagError verifies that an unknown flag produces
// cobra's error message.
// Covers: 08-REQ-1.E3
func TestTS08_01_UnknownFlagError(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--nonexistent-flag"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with unknown flag returned nil; want error")
	}
}

// --- NS-REQ-1/2/3: Verify the auto-injected cobra completion subcommand
//     is absent from the root command ---

// TestNSREQ1_CompletionSubcommandAbsent verifies that the root command has
// no registered subcommand named "completion".
// Covers: TS-NS-1, NS-REQ-1
func TestNSREQ1_CompletionSubcommandAbsent(t *testing.T) {
	cmd := newRootCmd()
	for _, sub := range cmd.Commands() {
		if sub.Name() == "completion" {
			t.Errorf("root command has a subcommand named %q; want it absent", sub.Name())
		}
	}
}

// TestNSREQ2_HelpOutputNoCompletion verifies that "--help" output does not
// list "completion" as an available subcommand.
// Covers: TS-NS-2, NS-REQ-2
func TestNSREQ2_HelpOutputNoCompletion(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--help"})

	_ = cmd.Execute()

	output := buf.String()
	if strings.Contains(output, "completion") {
		t.Errorf("--help output contains %q; want it absent\nfull output:\n%s", "completion", output)
	}
}

// TestNSREQ3_CompletionCommandUnknown verifies that invoking "completion"
// returns a non-nil error containing "unknown command".
// Covers: TS-NS-3, NS-REQ-3
func TestNSREQ3_CompletionCommandUnknown(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"completion"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with args [completion] returned nil error; want unknown-command error")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error = %q; want it to contain %q", err.Error(), "unknown command")
	}
}
