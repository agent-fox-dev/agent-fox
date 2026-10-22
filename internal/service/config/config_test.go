package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeFile writes a file under dir, which it makes, and returns its path.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// unsetenv unsets a variable for the rest of the test.
func unsetenv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "x")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

// minimal is a configuration that sets only hub.url and secrets.key_file.
func minimal(url string) string {
	return "[hub]\nurl = \"" + url + "\"\n[secrets]\nkey_file = \"key\"\n"
}

// loadBody writes body to a config.toml of its own and loads it.
func loadBody(t *testing.T, body string) (Config, error) {
	t.Helper()
	return Load(writeFile(t, t.TempDir(), "config.toml", body))
}

// mustLoad is loadBody for a configuration that is valid.
func mustLoad(t *testing.T, body string) Config {
	t.Helper()
	c, err := loadBody(t, body)
	if err != nil {
		t.Fatalf("Load: %v\n%s", err, body)
	}
	return c
}

// refuses fails the test unless loading body is an error that contains every term.
func refuses(t *testing.T, body string, terms ...string) {
	t.Helper()
	_, err := loadBody(t, body)
	if err == nil {
		t.Errorf("Load accepted:\n%s\nwant an error naming %q", body, terms)
		return
	}
	for _, term := range terms {
		if !strings.Contains(err.Error(), term) {
			t.Errorf("Load error = %q, want it to contain %q\n%s", err, term, body)
		}
	}
}

// TestLoadPrefersConfigThenWorkingDirectoryThenXDGThenHome_TS_11_9 verifies TS-11-9:
// Config lookup prefers --config, then ./config.toml, then XDG then HOME/.config.
// Verifies: 11-REQ-2.1
func TestLoadPrefersConfigThenWorkingDirectoryThenXDGThenHome_TS_11_9(t *testing.T) {
	root := t.TempDir()
	explicit := writeFile(t, filepath.Join(root, "explicit"), "config.toml", minimal("http://explicit.invalid"))
	local := filepath.Join(root, "local")
	writeFile(t, local, "config.toml", minimal("http://local.invalid"))
	xdg := filepath.Join(root, "xdg")
	writeFile(t, filepath.Join(xdg, "agent-fox"), "config.toml", minimal("http://xdg.invalid"))
	home := filepath.Join(root, "home")
	writeFile(t, filepath.Join(home, ".config", "agent-fox"), "config.toml", minimal("http://home.invalid"))
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", home)

	hubURL := func(explicit string) string {
		t.Helper()
		c, err := Load(explicit)
		if err != nil {
			t.Fatalf("Load(%q): %v", explicit, err)
		}
		return c.Hub.URL
	}

	t.Chdir(local)
	if got := hubURL(explicit); got != "http://explicit.invalid" {
		t.Errorf("an explicit path loaded %s, want it to win over ./config.toml and the XDG file", got)
	}
	if got := hubURL(""); got != "http://local.invalid" {
		t.Errorf("with ./config.toml present Load loaded %s, want it to win over the XDG file", got)
	}
	t.Chdir(empty)
	if got := hubURL(""); got != "http://xdg.invalid" {
		t.Errorf("with XDG_CONFIG_HOME set Load loaded %s, want $XDG_CONFIG_HOME/agent-fox/config.toml", got)
	}
	unsetenv(t, "XDG_CONFIG_HOME")
	if got := hubURL(""); got != "http://home.invalid" {
		t.Errorf("with XDG_CONFIG_HOME unset Load loaded %s, want $HOME/.config/agent-fox/config.toml", got)
	}
}

// TestAnEmptyOrRelativeXDGVariableIsAsIfUnset checks the XDG Base Directory rule that
// the lookup follows: a variable that is empty, or is not an absolute path, is unset.
// Verifies: 11-REQ-2.1, 11-REQ-2.3
func TestAnEmptyOrRelativeXDGVariableIsAsIfUnset(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeFile(t, filepath.Join(home, ".config", "agent-fox"), "config.toml", minimal("http://home.invalid"))
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	// A relative XDG_CONFIG_HOME that does hold a config, from the working directory.
	writeFile(t, filepath.Join(empty, "rel", "agent-fox"), "config.toml", minimal("http://relative.invalid"))
	t.Chdir(empty)
	t.Setenv("HOME", home)

	for _, value := range []string{"", "rel"} {
		t.Setenv("XDG_CONFIG_HOME", value)
		t.Setenv("XDG_STATE_HOME", value)
		c, err := Load("")
		if err != nil {
			t.Fatalf("XDG_CONFIG_HOME=%q: %v", value, err)
		}
		if c.Hub.URL != "http://home.invalid" {
			t.Errorf("XDG_CONFIG_HOME=%q loaded %s, want the config under $HOME/.config", value, c.Hub.URL)
		}
		if want := filepath.Join(home, ".local", "state", "agent-fox", "agent-fox.db"); c.Database.Path != want {
			t.Errorf("XDG_STATE_HOME=%q: database.path = %q, want %q under $HOME/.local/state", value, c.Database.Path, want)
		}
	}
}

// TestLoadFailsOnMissingFilesAndOnBadTOML_TS_11_10 verifies TS-11-10:
// A missing config lists every path tried and invalid TOML names the file and
// position. The exit code of the binary is TS-11-4's.
// Verifies: 11-REQ-2.2
func TestLoadFailsOnMissingFilesAndOnBadTOML_TS_11_10(t *testing.T) {
	root := t.TempDir()
	wd := filepath.Join(root, "wd")
	xdg := filepath.Join(root, "xdg")
	for _, dir := range []string{wd, xdg} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(wd)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("no file on the lookup path", func(t *testing.T) {
		_, err := Load("")
		if err == nil {
			t.Fatal("Load found a configuration where there is none")
		}
		for _, path := range []string{filepath.Join(cwd, "config.toml"), filepath.Join(xdg, "agent-fox", "config.toml")} {
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want it to list %s", err, path)
			}
		}
	})

	t.Run("the HOME fallback is listed when XDG_CONFIG_HOME is unset", func(t *testing.T) {
		home := filepath.Join(root, "home")
		t.Setenv("HOME", home)
		unsetenv(t, "XDG_CONFIG_HOME")
		_, err := Load("")
		if want := filepath.Join(home, ".config", "agent-fox", "config.toml"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to list %s", err, want)
		}
	})

	t.Run("a nonexistent --config", func(t *testing.T) {
		missing := filepath.Join(root, "nowhere", "c.toml")
		if _, err := Load(missing); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("error = %v, want it to name %s", err, missing)
		}
	})

	t.Run("a syntax error", func(t *testing.T) {
		bad := writeFile(t, root, "bad.toml", "[hub]\nurl = \"http://example.com\"\nsecrets = = 3\n")
		_, err := Load(bad)
		if err == nil {
			t.Fatal("Load accepted invalid TOML")
		}
		if !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), "line 3") {
			t.Errorf("error = %q, want the file and line 3", err)
		}
	})
}

// TestLoadAppliesDefaultsAndResolvesPaths_TS_11_11 verifies TS-11-11:
// A minimal config gets defaults, a trailing slash dropped and relative paths
// resolved against the config directory.
// Verifies: 11-REQ-2.3
func TestLoadAppliesDefaultsAndResolvesPaths_TS_11_11(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "config.toml", minimal("http://hub.example.com:9000/"))
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("with XDG_STATE_HOME", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_STATE_HOME", xdg)
		c, err := Load(file)
		if err != nil {
			t.Fatal(err)
		}
		if c.Server.Listen != "127.0.0.1:8090" {
			t.Errorf("server.listen = %q, want 127.0.0.1:8090", c.Server.Listen)
		}
		if c.Hub.AuthCacheTTL != 60*time.Second {
			t.Errorf("hub.auth_cache_ttl = %v, want 60s", c.Hub.AuthCacheTTL)
		}
		if c.Hub.URL != "http://hub.example.com:9000" {
			t.Errorf("hub.url = %q, want the trailing slash dropped", c.Hub.URL)
		}
		if want := filepath.Join(dir, "key"); c.Secrets.KeyFile != want {
			t.Errorf("secrets.key_file = %q, want %q", c.Secrets.KeyFile, want)
		}
		if want := filepath.Join(xdg, "agent-fox", "agent-fox.db"); c.Database.Path != want {
			t.Errorf("database.path = %q, want %q", c.Database.Path, want)
		}
		if c.Path != file {
			t.Errorf("Path = %q, want the file that was read, %q", c.Path, file)
		}
	})

	t.Run("without XDG_STATE_HOME", func(t *testing.T) {
		unsetenv(t, "XDG_STATE_HOME")
		c, err := Load(file)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(home, ".local", "state", "agent-fox", "agent-fox.db"); c.Database.Path != want {
			t.Errorf("database.path = %q, want %q", c.Database.Path, want)
		}
	})

	t.Run("a relative database.path", func(t *testing.T) {
		c := mustLoad(t, minimal("http://hub.example.com")+"[database]\npath = \"state/af.db\"\n")
		if !filepath.IsAbs(c.Database.Path) || !strings.HasSuffix(c.Database.Path, filepath.Join("state", "af.db")) {
			t.Errorf("database.path = %q, want an absolute path ending in state/af.db", c.Database.Path)
		}
		if filepath.Dir(filepath.Dir(c.Database.Path)) != filepath.Dir(c.Secrets.KeyFile) {
			t.Errorf("database.path %q and secrets.key_file %q are not both under the config directory", c.Database.Path, c.Secrets.KeyFile)
		}
	})

	t.Run("a relative --config", func(t *testing.T) {
		t.Chdir(dir)
		c, err := Load("config.toml")
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsAbs(c.Path) || !filepath.IsAbs(c.Secrets.KeyFile) {
			t.Errorf("Path %q and key file %q, want both absolute", c.Path, c.Secrets.KeyFile)
		}
	})

	t.Run("neither XDG_STATE_HOME nor HOME", func(t *testing.T) {
		unsetenv(t, "XDG_STATE_HOME")
		unsetenv(t, "HOME")
		if _, err := Load(file); err == nil || !strings.Contains(err.Error(), "database.path") {
			t.Errorf("error = %v, want one naming database.path, which has no default", err)
		}
	})
}

// TestLoadExpandsVariablesInStringValues_TS_11_12 verifies TS-11-12:
// ${NAME} is expanded in decoded string values and an empty variable expands
// to empty.
// Verifies: 11-REQ-2.4
func TestLoadExpandsVariablesInStringValues_TS_11_12(t *testing.T) {
	t.Setenv("HUBHOST", "hub.example.com")
	t.Setenv("KEYDIR", "/k")
	t.Setenv("EMPTYVAR", "")

	c := mustLoad(t, "[hub]\nurl = \"http://${HUBHOST}:1\"\n[secrets]\nkey_file = \"${KEYDIR}/key\"\n[database]\npath = \"/data/$notvar/x$\"\n")
	if c.Hub.URL != "http://hub.example.com:1" {
		t.Errorf("hub.url = %q, want http://hub.example.com:1", c.Hub.URL)
	}
	if c.Secrets.KeyFile != "/k/key" {
		t.Errorf("secrets.key_file = %q, want /k/key", c.Secrets.KeyFile)
	}
	if c.Database.Path != "/data/$notvar/x$" {
		t.Errorf("database.path = %q, want a $ that is not ${NAME} left alone", c.Database.Path)
	}

	_, err := loadBody(t, "[hub]\nurl = \"http://example.com\"\n[secrets]\nkey_file = \"${EMPTYVAR}\"\n")
	if err == nil || !strings.Contains(err.Error(), "secrets.key_file") || strings.Contains(err.Error(), "EMPTYVAR") {
		t.Errorf("error = %v, want secrets.key_file failing as missing, not as an unset variable", err)
	}

	t.Run("a variable in a value of the wrong shape is not expanded", func(t *testing.T) {
		t.Setenv("PORT", "8091")
		if c := mustLoad(t, minimal("http://example.com")+"[server]\nlisten = \"127.0.0.1:${PORT}\"\n"); c.Server.Listen != "127.0.0.1:8091" {
			t.Errorf("server.listen = %q, want 127.0.0.1:8091", c.Server.Listen)
		}
	})
}

// TestLoadRefusesAnUnsetVariable_TS_11_13 verifies the loader's half of TS-11-13:
// an unset ${VAR} is an error naming the variable and the dotted key. The exit
// code of the binary is checked with the start.
// Verifies: 11-REQ-2.5
func TestLoadRefusesAnUnsetVariable_TS_11_13(t *testing.T) {
	unsetenv(t, "NO_SUCH_VAR_XYZ")
	refuses(t, "[hub]\nurl = \"${NO_SUCH_VAR_XYZ}\"\n[secrets]\nkey_file = \"key\"\n", "NO_SUCH_VAR_XYZ", "hub.url")
	refuses(t, minimal("http://example.com")+"[database]\npath = \"/x/${NO_SUCH_VAR_XYZ}\"\n", "NO_SUCH_VAR_XYZ", "database.path")
}

// TestLoadRefusesUnknownKeysAndWrongTypes_TS_11_14 verifies the loader's half of
// TS-11-14: every key that is not defined, and every value of the wrong type,
// is an error naming the dotted key. The exit code of the binary is checked
// with the start.
// Verifies: 11-REQ-2.6
func TestLoadRefusesUnknownKeysAndWrongTypes_TS_11_14(t *testing.T) {
	for _, tc := range []struct{ name, body, key string }{
		{"server.external_url", minimal("http://example.com") + "[server]\nexternal_url = \"http://x.example.com\"\n", "server.external_url"},
		{"workers.limit", minimal("http://example.com") + "[workers]\nlimit = 2\n", "workers.limit"},
		{"database.retain that is not a duration", minimal("http://example.com") + "[database]\nretain = \"30d\"\n", "database.retain"},
		{"a misspelt key", "[hub]\nurl = \"http://example.com\"\nauth_cach_ttl = \"1m\"\n[secrets]\nkey_file = \"key\"\n", "hub.auth_cach_ttl"},
		{"an unknown table", minimal("http://example.com") + "[extra]\n", "extra"},
		{"hub.url of the wrong type", "[hub]\nurl = 5\n[secrets]\nkey_file = \"key\"\n", "hub.url"},
		{"hub.auth_cache_ttl of the wrong type", "[hub]\nurl = \"http://example.com\"\nauth_cache_ttl = 60\n[secrets]\nkey_file = \"key\"\n", "hub.auth_cache_ttl"},
		{"server.listen of the wrong type", minimal("http://example.com") + "[server]\nlisten = true\n", "server.listen"},
	} {
		t.Run(tc.name, func(t *testing.T) { refuses(t, tc.body, tc.key) })
	}
}

// TestLoadBoundsTheAuthCacheTTL_TS_11_15 verifies TS-11-15:
// auth_cache_ttl is accepted from 1s to 15m inclusive and refused outside,
// without clamping.
// Verifies: 11-REQ-2.7
func TestLoadBoundsTheAuthCacheTTL_TS_11_15(t *testing.T) {
	body := func(ttl string) string {
		return "[hub]\nurl = \"http://example.com\"\nauth_cache_ttl = \"" + ttl + "\"\n[secrets]\nkey_file = \"key\"\n"
	}
	for ttl, want := range map[string]time.Duration{"1s": time.Second, "15m": 15 * time.Minute, "90s": 90 * time.Second} {
		if got := mustLoad(t, body(ttl)).Hub.AuthCacheTTL; got != want {
			t.Errorf("auth_cache_ttl %q loaded as %v, want exactly %v", ttl, got, want)
		}
	}
	for _, ttl := range []string{"500ms", "16m", "15m1ns", "0s", "-1m", "x", ""} {
		c, err := loadBody(t, body(ttl))
		if err == nil || !strings.Contains(err.Error(), "hub.auth_cache_ttl") {
			t.Errorf("auth_cache_ttl %q: error = %v (ttl %v), want a refusal naming hub.auth_cache_ttl", ttl, err, c.Hub.AuthCacheTTL)
		}
	}
}

// TestLoadRefusesBadURLKeyFileAndListen_TS_11_16 verifies TS-11-16:
// Missing or malformed hub.url, key_file and server.listen are refused naming
// the dotted key.
// Verifies: 11-REQ-2.7
func TestLoadRefusesBadURLKeyFileAndListen_TS_11_16(t *testing.T) {
	const key = "[secrets]\nkey_file = \"key\"\n"
	const hub = "[hub]\nurl = \"http://example.com\"\n"
	for _, tc := range []struct{ name, body, key string }{
		{"hub.url missing", key, "hub.url"},
		{"hub.url empty", "[hub]\nurl = \"\"\n" + key, "hub.url"},
		{"hub.url relative", "[hub]\nurl = \"hub.example.com\"\n" + key, "hub.url"},
		{"hub.url without a host", "[hub]\nurl = \"http://\"\n" + key, "hub.url"},
		{"hub.url ftp", "[hub]\nurl = \"ftp://x.example.com\"\n" + key, "hub.url"},
		{"hub.url with a query", "[hub]\nurl = \"http://example.com/?q=1\"\n" + key, "hub.url"},
		{"hub.url with an empty query", "[hub]\nurl = \"http://example.com/p?\"\n" + key, "hub.url"},
		{"hub.url with a fragment", "[hub]\nurl = \"http://example.com/#frag\"\n" + key, "hub.url"},
		{"hub.url with an empty fragment", "[hub]\nurl = \"http://example.com/p#\"\n" + key, "hub.url"},
		{"secrets.key_file missing", hub, "secrets.key_file"},
		{"secrets.key_file empty", hub + "[secrets]\nkey_file = \"\"\n", "secrets.key_file"},
		{"server.listen without a port", hub + key + "[server]\nlisten = \"nocolon\"\n", "server.listen"},
		{"server.listen empty", hub + key + "[server]\nlisten = \"\"\n", "server.listen"},
		{"database.path empty", hub + key + "[database]\npath = \"\"\n", "database.path"},
	} {
		t.Run(tc.name, func(t *testing.T) { refuses(t, tc.body, tc.key) })
	}

	if got := mustLoad(t, "[hub]\nurl = \"https://h.example.com/prefix\"\n"+key).Hub.URL; got != "https://h.example.com/prefix" {
		t.Errorf("hub.url = %q, want a path prefix kept", got)
	}
	if got := mustLoad(t, "[hub]\nurl = \"https://h.example.com/prefix/\"\n"+key).Hub.URL; got != "https://h.example.com/prefix" {
		t.Errorf("hub.url = %q, want the trailing slash of a prefix dropped", got)
	}
	if got := mustLoad(t, hub+key+"[server]\nlisten = \"127.0.0.1:0\"\n").Server.Listen; got != "127.0.0.1:0" {
		t.Errorf("server.listen = %q, want port 0 accepted", got)
	}
}

// TestAnErrorAboutHubURLDoesNotRepeatItsValue checks that a hub.url which carries a
// credential and is refused does not put the credential in the error, which is
// logged at the start.
// Verifies: 11-REQ-3.6, 11-REQ-2.7
func TestAnErrorAboutHubURLDoesNotRepeatItsValue(t *testing.T) {
	for _, url := range []string{
		"http://user:hunter2@example.com/?q=1", "http://user:hunter2@example.com/#frag", "ftp://user:hunter2@example.com", "//user:hunter2@example.com",
	} {
		_, err := loadBody(t, "[hub]\nurl = \""+url+"\"\n[secrets]\nkey_file = \"key\"\n")
		if err == nil || !strings.Contains(err.Error(), "hub.url") {
			t.Errorf("%s: error = %v, want a refusal naming hub.url", url, err)
		} else if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: the error repeats the credential: %q", url, err)
		}
	}
}

// TestHubInClearForPlainHTTPToANonLoopbackHost checks the helper that the WARN
// of the start is written on, which is TS-11-17's.
// Verifies: 11-REQ-2.8
func TestHubInClearForPlainHTTPToANonLoopbackHost(t *testing.T) {
	for url, want := range map[string]bool{
		"http://hub.example.internal:8080": true,
		"http://192.0.2.5":                 true,
		"http://127.0.0.1:1":               false,
		"http://localhost:8080":            false,
		"http://[::1]:8080":                false,
		"https://hub.example.com":          false,
	} {
		if got := mustLoad(t, minimal(url)).HubInClear(); got != want {
			t.Errorf("HubInClear for %s = %v, want %v", url, got, want)
		}
	}
}

// TestWorkersAndRetainDefaultAndAcceptTheirBounds_TS_12_1 verifies TS-12-1:
// a config without [workers].count and [database].retain loads with 2 and 720h,
// and the boundary values of both keys are accepted.
// Verifies: 12-REQ-1.1
func TestWorkersAndRetainDefaultAndAcceptTheirBounds_TS_12_1(t *testing.T) {
	c := mustLoad(t, minimal("http://hub.example.com"))
	if c.Workers.Count != 2 || c.Database.Retain != 720*time.Hour {
		t.Errorf("defaults = workers %d, retain %v, want 2 and 720h", c.Workers.Count, c.Database.Retain)
	}

	for _, tc := range []struct {
		count  int
		retain string
		want   time.Duration
	}{
		{64, "1h", time.Hour},
		{1, "8760h", 8760 * time.Hour},
	} {
		c := mustLoad(t, minimal("http://hub.example.com")+"[workers]\ncount = "+strconv.Itoa(tc.count)+"\n[database]\nretain = \""+tc.retain+"\"\n")
		if c.Workers.Count != tc.count || c.Database.Retain != tc.want {
			t.Errorf("count %d, retain %s loaded as %d and %v, want %d and %v", tc.count, tc.retain, c.Workers.Count, c.Database.Retain, tc.count, tc.want)
		}
	}

	// A key alone leaves the other at its default, and the retain may name a variable.
	c = mustLoad(t, minimal("http://hub.example.com")+"[workers]\ncount = 5\n")
	if c.Workers.Count != 5 || c.Database.Retain != 720*time.Hour {
		t.Errorf("count alone: workers %d, retain %v, want 5 and 720h", c.Workers.Count, c.Database.Retain)
	}
	t.Setenv("AF_TEST_RETAIN", "48h")
	c = mustLoad(t, minimal("http://hub.example.com")+"[database]\nretain = \"${AF_TEST_RETAIN}\"\n")
	if c.Database.Retain != 48*time.Hour {
		t.Errorf("retain from a variable = %v, want 48h", c.Database.Retain)
	}
}

// TestWorkersAndRetainRefuseTheValuesOutsideTheirRangeNamingTheKey_TS_12_2 is the
// loader's half of TS-12-2: a count outside 1 to 64 or that is not an integer, and
// a retain that is malformed or outside 1h to 8760h, are each an error that names
// the dotted key and says what is wrong with the value, not that the key is unknown.
// Verifies: 12-REQ-1.2
func TestWorkersAndRetainRefuseTheValuesOutsideTheirRangeNamingTheKey_TS_12_2(t *testing.T) {
	base := minimal("http://hub.example.com")
	for name, tc := range map[string]struct{ body, key string }{
		"count 0":            {"[workers]\ncount = 0\n", "workers.count"},
		"count 65":           {"[workers]\ncount = 65\n", "workers.count"},
		"count -1":           {"[workers]\ncount = -1\n", "workers.count"},
		"count a string":     {"[workers]\ncount = \"two\"\n", "workers.count"},
		"count a fraction":   {"[workers]\ncount = 2.5\n", "workers.count"},
		"retain 30m":         {"[database]\nretain = \"30m\"\n", "database.retain"},
		"retain 8761h":       {"[database]\nretain = \"8761h\"\n", "database.retain"},
		"retain soon":        {"[database]\nretain = \"soon\"\n", "database.retain"},
		"retain a number":    {"[database]\nretain = 720\n", "database.retain"},
		"retain empty":       {"[database]\nretain = \"\"\n", "database.retain"},
		"retain unset var":   {"[database]\nretain = \"${AF_TEST_NO_SUCH_VARIABLE}\"\n", "database.retain"},
		"count and retain":   {"[workers]\ncount = 0\n[database]\nretain = \"soon\"\n", "workers.count"},
		"an unknown workers": {"[workers]\ncount = 2\nlimit = 3\n", "workers.limit"},
	} {
		t.Run(name, func(t *testing.T) {
			unsetenv(t, "AF_TEST_NO_SUCH_VARIABLE")
			_, err := loadBody(t, base+tc.body)
			if err == nil {
				t.Fatalf("Load accepted:\n%s", tc.body)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error = %q, want it to name %q", err, tc.key)
			}
			if name != "an unknown workers" && strings.Contains(err.Error(), "unknown key") {
				t.Errorf("error = %q, want a refusal of the value and not of the key", err)
			}
		})
	}
}
