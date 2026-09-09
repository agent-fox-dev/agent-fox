package agentspec

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// AgentSpecConfig holds model and provider configuration loaded from
// config.toml with optional environment variable overrides.
type AgentSpecConfig struct {
	Model         string
	ModelVariant  string // optional variant for tier resolution (e.g. "extended")
	AssessModel   string // optional per-phase override; empty means use Model
	RefineModel   string // optional per-phase override; empty means use Model
	GenerateModel string // optional per-phase override; empty means use Model
	AuthMethod    string
	VertexProject string
	VertexRegion  string
}

// ModelForPhase returns the model tier or ID to use for the given phase.
// It looks up the per-phase override field; when that field is empty (not
// configured), it falls back to the top-level Model value.
//
// Recognised phase names: "assess", "refine", "generate".
// Unknown phase names fall back to Model.
func (c AgentSpecConfig) ModelForPhase(phase string) string {
	switch phase {
	case "assess":
		if c.AssessModel != "" {
			return c.AssessModel
		}
	case "refine":
		if c.RefineModel != "" {
			return c.RefineModel
		}
	case "generate":
		if c.GenerateModel != "" {
			return c.GenerateModel
		}
	}
	return c.Model
}

// configFileModel maps to the [model] TOML section.
type configFileModel struct {
	Model         string `toml:"model"`
	ModelVariant  string `toml:"model_variant"`
	AssessModel   string `toml:"assess_model"`
	RefineModel   string `toml:"refine_model"`
	GenerateModel string `toml:"generate_model"`
}

// configFileProvider maps to the [provider] TOML section.
type configFileProvider struct {
	AuthMethod    string `toml:"auth_method"`
	VertexProject string `toml:"vertex_project"`
	VertexRegion  string `toml:"vertex_region"`
}

// configFile is the top-level TOML structure. Unknown sections are silently
// ignored because BurntSushi/toml does not error on unrecognised keys by
// default when we don't call MetaData.Undecoded.
type configFile struct {
	Model    configFileModel    `toml:"model"`
	Provider configFileProvider `toml:"provider"`
}

// LoadConfig searches for config.toml in .specs/ relative to the current
// working directory, then in ~/.specs/. It parses the first file found,
// reading [model] and [provider] sections. The AF_SPEC_MODEL environment
// variable overrides the Model field after all file-based loading.
func LoadConfig() (AgentSpecConfig, error) {
	cfg := AgentSpecConfig{
		Model: "STANDARD",
	}

	// Build candidate paths: local (.specs/) then global (~/.specs/).
	candidates := []string{}

	cwd, err := os.Getwd()
	if err == nil {
		candidates = append(candidates, filepath.Join(cwd, ".specs", "config.toml"))
	}

	home, err := os.UserHomeDir()
	if err == nil {
		candidates = append(candidates, filepath.Join(home, ".specs", "config.toml"))
	}

	// Try each candidate in order; use the first one found.
	for _, path := range candidates {
		found, loaded, loadErr := loadConfigFile(path)
		if loadErr != nil {
			return AgentSpecConfig{}, loadErr
		}
		if found {
			cfg = loaded
			break
		}
	}

	// Apply AF_SPEC_MODEL override if set to a non-empty value.
	if envModel := os.Getenv("AF_SPEC_MODEL"); envModel != "" {
		cfg.Model = envModel
	}

	return cfg, nil
}

// loadConfigFile attempts to load a config.toml from the given path.
// It returns (true, config, nil) if the file was found and parsed,
// (false, zero, nil) if the file does not exist, or (false, zero, err)
// on any error (symlink, permissions, parse failure).
func loadConfigFile(path string) (bool, AgentSpecConfig, error) {
	// Check if the file exists via Lstat (does not follow symlinks).
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, AgentSpecConfig{}, nil
		}
		// Permission error or other OS error.
		return false, AgentSpecConfig{}, &ConfigError{
			Msg:   fmt.Sprintf("cannot access config file %s: %v", path, err),
			Cause: err,
		}
	}

	// Reject symlinks: check if the config.toml file itself is a symlink.
	// We check the Lstat mode for the symlink bit, and also compare the
	// resolved path of the file against the resolved parent directory joined
	// with the filename (to avoid false positives from parent directory
	// symlinks, e.g. /var -> /private/var on macOS).
	if info.Mode()&os.ModeSymlink != 0 {
		return false, AgentSpecConfig{}, &ConfigError{
			Msg: fmt.Sprintf("config file is a symlink: %s", path),
		}
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, AgentSpecConfig{}, &ConfigError{
			Msg:   fmt.Sprintf("cannot resolve config file path %s: %v", path, err),
			Cause: err,
		}
	}

	// Resolve the parent directory separately and reconstruct the expected
	// path. If the fully-resolved path differs, the file itself is a symlink.
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false, AgentSpecConfig{}, &ConfigError{
			Msg:   fmt.Sprintf("cannot resolve config directory %s: %v", dir, err),
			Cause: err,
		}
	}
	expected := filepath.Join(resolvedDir, base)
	if resolved != expected {
		return false, AgentSpecConfig{}, &ConfigError{
			Msg: fmt.Sprintf(
				"config file resolved path differs from original (symlink detected): original=%s, resolved=%s",
				expected, resolved,
			),
		}
	}

	// Read the file contents.
	data, err := os.ReadFile(path)
	if err != nil {
		return false, AgentSpecConfig{}, &ConfigError{
			Msg:   fmt.Sprintf("cannot read config file %s: %v", path, err),
			Cause: err,
		}
	}

	// Parse TOML.
	var cf configFile
	if err := toml.Unmarshal(data, &cf); err != nil {
		return false, AgentSpecConfig{}, &ConfigError{
			Msg:   fmt.Sprintf("invalid TOML in config file %s: %v", path, err),
			Cause: err,
		}
	}

	cfg := AgentSpecConfig{
		Model:         cf.Model.Model,
		ModelVariant:  cf.Model.ModelVariant,
		AssessModel:   cf.Model.AssessModel,
		RefineModel:   cf.Model.RefineModel,
		GenerateModel: cf.Model.GenerateModel,
		AuthMethod:    cf.Provider.AuthMethod,
		VertexProject: cf.Provider.VertexProject,
		VertexRegion:  cf.Provider.VertexRegion,
	}

	// Apply default for Model if not specified in config file.
	if cfg.Model == "" {
		cfg.Model = "STANDARD"
	}

	return true, cfg, nil
}
