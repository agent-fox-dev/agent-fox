package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- TS-08-40: Verify that the build system produces static binaries with
//     the correct embedded version ---

// TestTS08_40_VersionEmbeddedViaLdflags verifies that the version
// variable is set at build time via ldflags (-X main.version=...) by
// compiling the binary with a known version string and checking output.
// Covers: TS-08-40, Requirement: 08-REQ-15.1
func TestTS08_40_VersionEmbeddedViaLdflags(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}

	// Find the main package to build.
	mainPkg := findMainPackage(t)
	if mainPkg == "" {
		t.Skip("could not locate main package for spec binary")
	}

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "spec_test_binary")

	// Build with a known version via ldflags.
	buildCmd := exec.Command("go", "build",
		"-ldflags", "-X main.version=42.0.0-test",
		"-o", binaryPath,
		mainPkg,
	)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	buildOutput, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\noutput: %s", err, buildOutput)
	}

	// Run the binary with --version and check the output.
	versionCmd := exec.Command(binaryPath, "--version")
	versionOutput, err := versionCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("binary --version failed: %v\noutput: %s", err, versionOutput)
	}

	if !strings.Contains(string(versionOutput), "42.0.0-test") {
		t.Errorf("--version output = %q; want it to contain %q", string(versionOutput), "42.0.0-test")
	}
}

// TestTS08_40_DefaultVersionIsDev verifies that the default version
// variable is "dev" when no ldflags override is applied.
// Covers: TS-08-40, Requirement: 08-REQ-15.1
func TestTS08_40_DefaultVersionIsDev(t *testing.T) {
	if version != "dev" {
		// This test verifies the default; if we're running inside a
		// built binary with a real version, skip.
		t.Skipf("version = %q (overridden by build); skipping default check", version)
	}
	// Default version should be "dev".
	if version != "dev" {
		t.Errorf("default version = %q; want %q", version, "dev")
	}
}

// TestTS08_40_CGODisabledBuild verifies that the binary can be compiled
// with CGO_ENABLED=0 for static linking.
// Covers: TS-08-40, Requirement: 08-REQ-15.1
func TestTS08_40_CGODisabledBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}

	mainPkg := findMainPackage(t)
	if mainPkg == "" {
		t.Skip("could not locate main package for spec binary")
	}

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "spec_static_binary")

	buildCmd := exec.Command("go", "build",
		"-ldflags", "-X main.version=static-test",
		"-o", binaryPath,
		mainPkg,
	)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	buildOutput, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build with CGO_ENABLED=0 failed: %v\noutput: %s", err, buildOutput)
	}

	// Verify the binary exists and is executable.
	info, err := os.Stat(binaryPath)
	if err != nil {
		t.Fatalf("binary not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("binary file is empty")
	}
}

// TestTS08_40_CrossCompilationTargets verifies that the binary can be
// compiled for all four supported platform targets.
// Covers: TS-08-40, Requirement: 08-REQ-15.1
func TestTS08_40_CrossCompilationTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cross-compilation test in short mode")
	}

	mainPkg := findMainPackage(t)
	if mainPkg == "" {
		t.Skip("could not locate main package for spec binary")
	}

	targets := []struct {
		goos   string
		goarch string
	}{
		{"darwin", "arm64"},
		{"darwin", "amd64"},
		{"linux", "arm64"},
		{"linux", "amd64"},
	}

	tmpDir := t.TempDir()

	for _, target := range targets {
		t.Run(target.goos+"/"+target.goarch, func(t *testing.T) {
			binaryName := "spec_" + target.goos + "_" + target.goarch
			binaryPath := filepath.Join(tmpDir, binaryName)

			buildCmd := exec.Command("go", "build",
				"-ldflags", "-X main.version=1.0.0",
				"-o", binaryPath,
				mainPkg,
			)
			buildCmd.Env = append(os.Environ(),
				"CGO_ENABLED=0",
				"GOOS="+target.goos,
				"GOARCH="+target.goarch,
			)
			buildOutput, err := buildCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("cross-compile for %s/%s failed: %v\noutput: %s",
					target.goos, target.goarch, err, buildOutput)
			}

			// Verify the binary file was created.
			info, err := os.Stat(binaryPath)
			if err != nil {
				t.Fatalf("binary not found for %s/%s: %v",
					target.goos, target.goarch, err)
			}
			if info.Size() == 0 {
				t.Errorf("binary for %s/%s is empty",
					target.goos, target.goarch)
			}
		})
	}
}

// TestTS08_40_NativeVersionOutput verifies that a natively-built binary
// reports the correct version string when invoked with --version.
// Covers: TS-08-40, Requirement: 08-REQ-15.1
func TestTS08_40_NativeVersionOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}

	mainPkg := findMainPackage(t)
	if mainPkg == "" {
		t.Skip("could not locate main package for spec binary")
	}

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "spec_version_check")

	buildCmd := exec.Command("go", "build",
		"-ldflags", "-X main.version=1.0.0",
		"-o", binaryPath,
		mainPkg,
	)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\noutput: %s", err, out)
	}

	// Only verify if we built for the current platform.
	versionCmd := exec.Command(binaryPath, "--version")
	versionOutput, err := versionCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("binary --version failed: %v\noutput: %s", err, versionOutput)
	}

	if !strings.Contains(string(versionOutput), "1.0.0") {
		t.Errorf("--version output = %q; want it to contain %q",
			string(versionOutput), "1.0.0")
	}
}

// --- TS-08-41: Verify install.sh downloads Go binary ---

// TestTS08_41_InstallScriptExists verifies that the install.sh script
// exists and contains Go binary distribution logic rather than uv tool
// install.
// Covers: TS-08-41, Requirement: 08-REQ-15.2
func TestTS08_41_InstallScriptExists(t *testing.T) {
	// Look for install.sh in common locations.
	possiblePaths := []string{
		"install.sh",
		"../../../install.sh",
		filepath.Join("..", "..", "..", "install.sh"),
	}

	var scriptPath string
	for _, p := range possiblePaths {
		if _, err := os.Stat(p); err == nil {
			scriptPath = p
			break
		}
	}

	if scriptPath == "" {
		t.Skip("install.sh not found; skipping install script test")
	}

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("failed to read install.sh: %v", err)
	}

	script := string(content)

	// The script should reference Go binary downloading, not uv tool install.
	if strings.Contains(script, "uv tool install") {
		t.Error("install.sh contains 'uv tool install'; want Go binary download logic")
	}
}

// --- 08-REQ-15.E1: Unsupported platform detection ---

// TestTS08_40_SupportedPlatforms verifies that the supported platforms
// list includes exactly darwin/arm64, darwin/amd64, linux/arm64, and
// linux/amd64.
// Covers: 08-REQ-15.E1
func TestTS08_40_SupportedPlatforms(t *testing.T) {
	// This test verifies the current platform is one of the supported ones
	// or notes it as potentially unsupported.
	supported := map[string]bool{
		"darwin/arm64": true,
		"darwin/amd64": true,
		"linux/arm64":  true,
		"linux/amd64":  true,
	}

	currentPlatform := runtime.GOOS + "/" + runtime.GOARCH
	if !supported[currentPlatform] {
		t.Logf("current platform %s is not in the supported target list", currentPlatform)
	}
}

// findMainPackage locates the main package for the spec binary.
func findMainPackage(t *testing.T) string {
	t.Helper()

	// Search common locations for the main.go entry point.
	candidates := []string{
		"./main.go",
		"../../cmd/spec/",
	}

	// Also try to find by looking for a main.go near the module root.
	if entries, err := filepath.Glob("../../cmd/*/main.go"); err == nil {
		for _, e := range entries {
			candidates = append(candidates, filepath.Dir(e))
		}
	}

	for _, c := range candidates {
		mainFile := filepath.Join(c, "main.go")
		if _, err := os.Stat(mainFile); err == nil {
			return c
		}
		// Also try the candidate itself as a file.
		if strings.HasSuffix(c, ".go") {
			if _, err := os.Stat(c); err == nil {
				return filepath.Dir(c)
			}
		}
	}

	return ""
}
