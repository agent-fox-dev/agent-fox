package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/config"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestLoad_LooksForTheFileInOrder(t *testing.T) {
	root := t.TempDir()
	explicit := testutil.WriteFile(t, root, "explicit/config.toml", minimal("http://explicit.invalid"))
	testutil.WriteFile(t, root, "local/config.toml", minimal("http://local.invalid"))
	testutil.WriteFile(t, root, "xdg/agent-fox/config.toml", minimal("http://xdg.invalid"))
	testutil.WriteFile(t, root, "home/.config/agent-fox/config.toml", minimal("http://home.invalid"))
	testutil.WriteFile(t, root, "empty/rel/agent-fox/config.toml", minimal("http://relative.invalid"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	t.Setenv("HOME", filepath.Join(root, "home"))

	hubURL := func(explicit string) string {
		t.Helper()
		c, err := config.Load(explicit)
		if err != nil {
			t.Fatalf("Load(%q): %v", explicit, err)
		}
		return c.Hub.URL
	}
	t.Chdir(filepath.Join(root, "local"))
	if got := hubURL(explicit); got != "http://explicit.invalid" {
		t.Errorf("an explicit path loaded %s", got)
	}
	if got := hubURL(""); got != "http://local.invalid" {
		t.Errorf("with ./config.toml present Load loaded %s", got)
	}
	t.Chdir(filepath.Join(root, "empty"))
	if got := hubURL(""); got != "http://xdg.invalid" {
		t.Errorf("with XDG_CONFIG_HOME set Load loaded %s", got)
	}
	for _, xdg := range []string{"", "rel"} { // an empty or relative XDG variable is as if unset
		t.Setenv("XDG_CONFIG_HOME", xdg)
		if got := hubURL(""); got != "http://home.invalid" {
			t.Errorf("with XDG_CONFIG_HOME=%q Load loaded %s, want the file under $HOME/.config", xdg, got)
		}
	}
}

func TestLoad_ListsEveryPathTriedWhenNoFileIsFound(t *testing.T) {
	root := t.TempDir()
	wd, xdg, home := filepath.Join(root, "wd"), filepath.Join(root, "xdg"), filepath.Join(root, "home")
	for _, dir := range []string{wd, xdg} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(wd)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	_, err = config.Load("")
	for _, path := range []string{filepath.Join(cwd, "config.toml"), filepath.Join(xdg, "agent-fox", "config.toml"), "--config"} {
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("error = %v, want it to list %s", err, path)
		}
	}
	unsetenv(t, "XDG_CONFIG_HOME")
	if _, err := config.Load(""); err == nil || !strings.Contains(err.Error(), filepath.Join(home, ".config", "agent-fox", "config.toml")) {
		t.Errorf("error = %v, want it to list the file under HOME", err)
	}
}

func TestLoad_RefusesAnExplicitFileThatIsNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere", "c.toml")
	if _, err := config.Load(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("error = %v, want it to name %s", err, missing)
	}
}
