package gitx

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agentfox/agentkit-go/tools"
)

// HermeticRunner is ReducedEnvRunner in a clean environment: HOME is home (an
// empty directory the caller owns), git reads no global and no system
// configuration, and the variables that inject git configuration or an
// identity are removed.
//
// It is what a final verification runs under. A suite that passes on the
// author's machine because ~/.gitconfig sets init.defaultBranch=main, or a
// user.email, fails on a clean CI image; under this runner it fails here
// first, before a pull request says the checks pass.
//
// The language toolchains' download caches are pointed at where they already
// are, so a clean HOME does not mean downloading every module again: the
// caches hold content-addressed downloads, not configuration.
func HermeticRunner(home string) Runner {
	env := HermeticEnv(tools.ReducedEnv(nil), home)
	return func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		return run(ctx, dir, argv, env, stdin...)
	}
}

// HermeticEnv rewrites base for a run with home as HOME and no git
// configuration beyond the repository's own.
func HermeticEnv(base []string, home string) []string {
	get := func(name string) string {
		for _, kv := range base {
			if k, v, ok := strings.Cut(kv, "="); ok && k == name {
				return v
			}
		}
		return ""
	}
	realHome := get("HOME")
	pinned := toolchainCaches(get, realHome)

	out := make([]string, 0, len(base)+len(pinned)+4)
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if dropInHermetic(name) {
			continue
		}
		if _, ok := pinned[name]; ok {
			continue
		}
		out = append(out, kv)
	}
	for _, name := range slices.Sorted(maps.Keys(pinned)) {
		out = append(out, name+"="+pinned[name])
	}
	return append(out,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

// dropInHermetic names the variables a clean environment does not have: the
// ones replaced below, and every way git can be handed configuration or an
// identity from outside the repository.
func dropInHermetic(name string) bool {
	switch name {
	case "HOME", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM",
		"GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_DIR", "GIT_WORK_TREE",
		"GIT_INDEX_FILE", "GIT_TEMPLATE_DIR", "EMAIL":
		return true
	}
	for _, p := range []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_", "GIT_AUTHOR_", "GIT_COMMITTER_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// toolchainCaches resolves the download caches that live under HOME by
// default, so that moving HOME does not move them. A variable already set is
// kept as it is; a default directory that does not exist is not invented.
func toolchainCaches(get func(string) string, realHome string) map[string]string {
	out := map[string]string{}
	set := func(name, dflt string) {
		if v := get(name); v != "" {
			out[name] = v
			return
		}
		if dflt == "" {
			return
		}
		if _, err := os.Stat(dflt); err == nil {
			out[name] = dflt
		}
	}
	cache, _ := os.UserCacheDir()
	if realHome != "" {
		set("GOPATH", filepath.Join(realHome, "go"))
		set("CARGO_HOME", filepath.Join(realHome, ".cargo"))
		set("RUSTUP_HOME", filepath.Join(realHome, ".rustup"))
		set("npm_config_cache", filepath.Join(realHome, ".npm"))
	}
	if gopath := out["GOPATH"]; gopath != "" {
		set("GOMODCACHE", filepath.Join(gopath, "pkg", "mod"))
	}
	if cache != "" {
		set("GOCACHE", filepath.Join(cache, "go-build"))
		set("UV_CACHE_DIR", filepath.Join(cache, "uv"))
		set("PIP_CACHE_DIR", filepath.Join(cache, "pip"))
	}
	return out
}
