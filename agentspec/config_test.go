package agentspec

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeTestFile creates a file at the given path with the given content,
// creating parent directories as needed.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// chdirTemp changes the working directory to dir and restores it on cleanup.
func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

// TestTS06_06_LoadConfigLocal verifies that LoadConfig reads config.toml
// from .specs/ relative to the working directory, parses [model] and
// [provider] sections, and silently ignores unknown sections.
// Test Spec: TS-06-6, Requirement: 06-REQ-2.1
func TestTS06_06_LoadConfigLocal(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `[model]
model = "FAST"

[provider]
auth_method = "vertex"
vertex_project = "proj"
vertex_region = "us-east1"

[theme]
color = "blue"
`
	writeTestFile(t, filepath.Join(tmpDir, ".specs", "config.toml"), configContent)
	chdirTemp(t, tmpDir)

	// Redirect HOME to prevent real global config from interfering.
	t.Setenv("HOME", t.TempDir())
	// Ensure AF_SPEC_MODEL is unset (empty string treated as unset per spec).
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	if cfg.Model != "FAST" {
		t.Errorf("Model = %q; want %q", cfg.Model, "FAST")
	}
	if cfg.AuthMethod != "vertex" {
		t.Errorf("AuthMethod = %q; want %q", cfg.AuthMethod, "vertex")
	}
	if cfg.VertexProject != "proj" {
		t.Errorf("VertexProject = %q; want %q", cfg.VertexProject, "proj")
	}
	if cfg.VertexRegion != "us-east1" {
		t.Errorf("VertexRegion = %q; want %q", cfg.VertexRegion, "us-east1")
	}
}

// TestTS06_07_LoadConfigDefaults verifies that LoadConfig returns an
// AgentSpecConfig with Model set to "STANDARD" and other fields empty
// when no config.toml exists.
// Test Spec: TS-06-7, Requirement: 06-REQ-2.2
func TestTS06_07_LoadConfigDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	chdirTemp(t, tmpDir)

	// Redirect HOME to an empty directory so no global config is found.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	if cfg.Model != "STANDARD" {
		t.Errorf("Model = %q; want %q", cfg.Model, "STANDARD")
	}
	if cfg.AuthMethod != "" {
		t.Errorf("AuthMethod = %q; want empty string", cfg.AuthMethod)
	}
	if cfg.VertexProject != "" {
		t.Errorf("VertexProject = %q; want empty string", cfg.VertexProject)
	}
	if cfg.VertexRegion != "" {
		t.Errorf("VertexRegion = %q; want empty string", cfg.VertexRegion)
	}
}

// TestTS06_08_LoadConfigEnvOverride verifies that when AF_SPEC_MODEL is set
// to a non-empty value, it overrides the Model field from the config file.
// Test Spec: TS-06-8, Requirement: 06-REQ-2.3
func TestTS06_08_LoadConfigEnvOverride(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `[model]
model = "STANDARD"
`
	writeTestFile(t, filepath.Join(tmpDir, ".specs", "config.toml"), configContent)
	chdirTemp(t, tmpDir)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("AF_SPEC_MODEL", "FAST")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	if cfg.Model != "FAST" {
		t.Errorf("Model = %q; want %q (from AF_SPEC_MODEL)", cfg.Model, "FAST")
	}
}

// TestTS06_09_LoadConfigSymlink verifies that LoadConfig returns a
// ConfigError when config.toml is a symlink.
// Test Spec: TS-06-9, Requirement: 06-REQ-2.4
func TestTS06_09_LoadConfigSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not supported on Windows")
	}

	tmpDir := t.TempDir()

	// Create the real config file at a different location.
	realConfig := filepath.Join(tmpDir, "real_config.toml")
	writeTestFile(t, realConfig, `[model]
model = "FAST"
`)

	// Create .specs/ directory and symlink config.toml to the real file.
	specsDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(specsDir, "config.toml")
	if err := os.Symlink(realConfig, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	chdirTemp(t, tmpDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()

	// Expect a ConfigError.
	if err == nil {
		t.Fatalf("LoadConfig() returned nil error; want ConfigError (got cfg=%+v)", cfg)
	}
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *ConfigError", err)
	}
	if ce.Category() != "config" {
		t.Errorf("ConfigError.Category() = %q; want %q", ce.Category(), "config")
	}
	// The error message should mention symlink or resolved path.
	msg := ce.Error()
	if !strings.Contains(strings.ToLower(msg), "symlink") &&
		!strings.Contains(strings.ToLower(msg), "resolved path") {
		t.Errorf("error message %q should mention symlink or resolved path", msg)
	}
}

// TestTS06_10_LoadConfigInvalidTOML verifies that LoadConfig returns a
// ConfigError wrapping the parse error when config.toml has invalid syntax.
// Test Spec: TS-06-10, Requirement: 06-REQ-2.5
func TestTS06_10_LoadConfigInvalidTOML(t *testing.T) {
	tmpDir := t.TempDir()

	// Write config.toml with invalid TOML syntax.
	invalidContent := "[model\nmodel = @@invalid@@"
	writeTestFile(t, filepath.Join(tmpDir, ".specs", "config.toml"), invalidContent)
	chdirTemp(t, tmpDir)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()

	// Expect a ConfigError wrapping the parse error.
	if err == nil {
		t.Fatalf("LoadConfig() returned nil error; want ConfigError (got cfg=%+v)", cfg)
	}
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *ConfigError", err)
	}
	if ce.Category() != "config" {
		t.Errorf("ConfigError.Category() = %q; want %q", ce.Category(), "config")
	}
	// ConfigError should wrap the TOML parse error.
	if errors.Unwrap(ce) == nil {
		t.Error("ConfigError.Unwrap() returned nil; want wrapped TOML parse error")
	}
}

// TestLoadConfig_LocalPrecedence verifies that when both .specs/config.toml
// and ~/.specs/config.toml exist, only the local file is used.
// Edge Case: 06-REQ-2.E1
func TestLoadConfig_LocalPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := t.TempDir()

	// Local config with model "LOCAL".
	localConfig := `[model]
model = "LOCAL"
`
	writeTestFile(t, filepath.Join(tmpDir, ".specs", "config.toml"), localConfig)

	// Global config with model "GLOBAL".
	globalConfig := `[model]
model = "GLOBAL"
`
	writeTestFile(t, filepath.Join(homeDir, ".specs", "config.toml"), globalConfig)

	chdirTemp(t, tmpDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	// Local config should take precedence; "GLOBAL" should be ignored.
	if cfg.Model != "LOCAL" {
		t.Errorf("Model = %q; want %q (local should take precedence over global)", cfg.Model, "LOCAL")
	}
}

// TestLoadConfig_EmptyEnvVar verifies that when AF_SPEC_MODEL is set to an
// empty string, it is treated as unset and the config file value is used.
// Edge Case: 06-REQ-2.E3
func TestLoadConfig_EmptyEnvVar(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `[model]
model = "FROM_FILE"
`
	writeTestFile(t, filepath.Join(tmpDir, ".specs", "config.toml"), configContent)
	chdirTemp(t, tmpDir)

	t.Setenv("HOME", t.TempDir())
	// Set AF_SPEC_MODEL to empty string -- should be treated as unset.
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	if cfg.Model != "FROM_FILE" {
		t.Errorf("Model = %q; want %q (empty AF_SPEC_MODEL should be treated as unset)", cfg.Model, "FROM_FILE")
	}
}

// TestLoadConfig_UnreadableFile verifies that LoadConfig returns a
// ConfigError when config.toml exists but is not readable due to
// filesystem permissions.
// Edge Case: 06-REQ-2.E4
func TestLoadConfig_UnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission test not reliable on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("test skipped when running as root (permissions not enforced)")
	}

	tmpDir := t.TempDir()

	// Create config.toml with no read permissions.
	configPath := filepath.Join(tmpDir, ".specs", "config.toml")
	writeTestFile(t, configPath, `[model]
model = "SECRET"
`)
	if err := os.Chmod(configPath, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(configPath, 0o644) })

	chdirTemp(t, tmpDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()

	if err == nil {
		t.Fatalf("LoadConfig() returned nil error; want ConfigError (got cfg=%+v)", cfg)
	}
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *ConfigError", err)
	}
	if ce.Category() != "config" {
		t.Errorf("ConfigError.Category() = %q; want %q", ce.Category(), "config")
	}
}

// TestLoadConfig_GlobalFallback verifies that LoadConfig falls back to
// ~/.specs/config.toml when no local .specs/config.toml exists.
// Requirement: 06-REQ-2.1
func TestLoadConfig_GlobalFallback(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := t.TempDir()

	// No local config -- only global.
	globalConfig := `[model]
model = "GLOBAL_ONLY"

[provider]
auth_method = "api_key"
`
	writeTestFile(t, filepath.Join(homeDir, ".specs", "config.toml"), globalConfig)

	chdirTemp(t, tmpDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	if cfg.Model != "GLOBAL_ONLY" {
		t.Errorf("Model = %q; want %q (should fall back to global config)", cfg.Model, "GLOBAL_ONLY")
	}
	if cfg.AuthMethod != "api_key" {
		t.Errorf("AuthMethod = %q; want %q", cfg.AuthMethod, "api_key")
	}
}

// TestLoadConfig_EnvOverrideWithoutFile verifies that AF_SPEC_MODEL applies
// even when no config.toml exists anywhere.
// Requirement: 06-REQ-2.3
func TestLoadConfig_EnvOverrideWithoutFile(t *testing.T) {
	tmpDir := t.TempDir()
	chdirTemp(t, tmpDir)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("AF_SPEC_MODEL", "ENV_ONLY")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	if cfg.Model != "ENV_ONLY" {
		t.Errorf("Model = %q; want %q (AF_SPEC_MODEL should apply without config file)", cfg.Model, "ENV_ONLY")
	}
}

// TestTS_NS_5_PerPhaseModelFields verifies that LoadConfig reads
// assess_model, refine_model, and generate_model from the [model] section
// and that ModelForPhase returns the correct tier for each phase.
// Test Spec: TS-NS-5, Requirement: NS-REQ-5
func TestTS_NS_5_PerPhaseModelFields(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `[model]
model = "ADVANCED"
assess_model = "SIMPLE"
generate_model = "STANDARD"
`
	writeTestFile(t, filepath.Join(tmpDir, ".specs", "config.toml"), configContent)
	chdirTemp(t, tmpDir)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	// Top-level model.
	if cfg.Model != "ADVANCED" {
		t.Errorf("Model = %q; want %q", cfg.Model, "ADVANCED")
	}
	// Per-phase overrides.
	if cfg.AssessModel != "SIMPLE" {
		t.Errorf("AssessModel = %q; want %q", cfg.AssessModel, "SIMPLE")
	}
	if cfg.RefineModel != "" {
		t.Errorf("RefineModel = %q; want empty (not set)", cfg.RefineModel)
	}
	if cfg.GenerateModel != "STANDARD" {
		t.Errorf("GenerateModel = %q; want %q", cfg.GenerateModel, "STANDARD")
	}
	// ModelForPhase resolution.
	if got := cfg.ModelForPhase("assess"); got != "SIMPLE" {
		t.Errorf("ModelForPhase(assess) = %q; want %q", got, "SIMPLE")
	}
	if got := cfg.ModelForPhase("refine"); got != "ADVANCED" {
		t.Errorf("ModelForPhase(refine) = %q; want %q (should inherit Model)", got, "ADVANCED")
	}
	if got := cfg.ModelForPhase("generate"); got != "STANDARD" {
		t.Errorf("ModelForPhase(generate) = %q; want %q", got, "STANDARD")
	}
}

// TestTS_NS_3_BackwardCompatNoPerPhaseFields verifies that when no per-phase
// fields are set, all phases return the top-level model (backward compat).
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestTS_NS_3_BackwardCompatNoPerPhaseFields(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `[model]
model = "ADVANCED"
`
	writeTestFile(t, filepath.Join(tmpDir, ".specs", "config.toml"), configContent)
	chdirTemp(t, tmpDir)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	for _, phase := range []string{"assess", "refine", "generate", "unknown"} {
		if got := cfg.ModelForPhase(phase); got != "ADVANCED" {
			t.Errorf("ModelForPhase(%q) = %q; want %q", phase, got, "ADVANCED")
		}
	}
}

// TestTS_NS_4_PerPhaseAcceptsModelID verifies that per-phase fields accept
// direct model IDs (not only tier names).
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestTS_NS_4_PerPhaseAcceptsModelID(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `[model]
model = "STANDARD"
assess_model = "claude-haiku-4-5"
`
	writeTestFile(t, filepath.Join(tmpDir, ".specs", "config.toml"), configContent)
	chdirTemp(t, tmpDir)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	if cfg.AssessModel != "claude-haiku-4-5" {
		t.Errorf("AssessModel = %q; want %q", cfg.AssessModel, "claude-haiku-4-5")
	}
	if got := cfg.ModelForPhase("assess"); got != "claude-haiku-4-5" {
		t.Errorf("ModelForPhase(assess) = %q; want %q", got, "claude-haiku-4-5")
	}
}

// TestLoadConfig_ModelVariant verifies that model_variant is parsed from the
// [model] section of config.toml.
func TestLoadConfig_ModelVariant(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `[model]
model = "ADVANCED"
model_variant = "extended"
`
	writeTestFile(t, filepath.Join(tmpDir, ".specs", "config.toml"), configContent)
	chdirTemp(t, tmpDir)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	if cfg.ModelVariant != "extended" {
		t.Errorf("ModelVariant = %q; want %q", cfg.ModelVariant, "extended")
	}
}

// TestLoadConfig_ModelVariantDefaultEmpty verifies that ModelVariant defaults
// to an empty string when not set.
func TestLoadConfig_ModelVariantDefaultEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `[model]
model = "ADVANCED"
`
	writeTestFile(t, filepath.Join(tmpDir, ".specs", "config.toml"), configContent)
	chdirTemp(t, tmpDir)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("AF_SPEC_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	if cfg.ModelVariant != "" {
		t.Errorf("ModelVariant = %q; want empty string", cfg.ModelVariant)
	}
}

// TestModelForPhase_FallbackForUnknownPhase verifies that ModelForPhase
// returns the top-level Model when called with an unrecognised phase name.
func TestModelForPhase_FallbackForUnknownPhase(t *testing.T) {
	cfg := AgentSpecConfig{
		Model:         "STANDARD",
		AssessModel:   "SIMPLE",
		GenerateModel: "ADVANCED",
	}

	if got := cfg.ModelForPhase("unknown"); got != "STANDARD" {
		t.Errorf("ModelForPhase(unknown) = %q; want %q", got, "STANDARD")
	}
	if got := cfg.ModelForPhase(""); got != "STANDARD" {
		t.Errorf("ModelForPhase(\"\") = %q; want %q", got, "STANDARD")
	}
}
