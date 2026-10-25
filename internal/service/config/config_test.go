package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/config"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// minimal is a configuration that sets only the two required keys.
func minimal(url string) string {
	return "[hub]\nurl = \"" + url + "\"\n[secrets]\nkey_file = \"key\"\n"
}

// load writes body to a config.toml of its own and loads it.
func load(t *testing.T, body string) (config.Config, error) {
	t.Helper()
	return config.Load(testutil.WriteFile(t, t.TempDir(), "config.toml", body))
}

func mustLoad(t *testing.T, body string) config.Config {
	t.Helper()
	c, err := load(t, body)
	if err != nil {
		t.Fatalf("Load: %v\n%s", err, body)
	}
	return c
}

// unsetenv unsets a variable for the rest of the test.
func unsetenv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "x")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_AppliesTheDefaultsAndResolvesThePaths(t *testing.T) {
	dir := t.TempDir()
	file := testutil.WriteFile(t, dir, "config.toml", minimal("http://hub.example.com:9000/"))
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	c, err := config.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		Path:     file,
		Server:   config.Server{Listen: "127.0.0.1:8090"},
		Hub:      config.Hub{URL: "http://hub.example.com:9000", AuthCacheTTL: 60 * time.Second},
		Database: config.Database{Path: filepath.Join(state, "agent-fox", "agent-fox.db"), Retain: 720 * time.Hour},
		Workers:  config.Workers{Count: 2},
		Secrets:  config.Secrets{KeyFile: filepath.Join(dir, "key")},
	}
	if c != want {
		t.Errorf("Load = %+v\nwant %+v", c, want)
	}

	t.Run("a relative database.path is under the config's directory", func(t *testing.T) {
		c := mustLoad(t, minimal("http://hub.example.com")+"[database]\npath = \"state/af.db\"\n")
		if want := filepath.Join(filepath.Dir(c.Path), "state", "af.db"); c.Database.Path != want {
			t.Errorf("database.path = %q, want %q", c.Database.Path, want)
		}
	})
	t.Run("a relative --config gives absolute paths", func(t *testing.T) {
		t.Chdir(dir)
		c, err := config.Load("config.toml")
		if err != nil || c.Path != file || c.Secrets.KeyFile != filepath.Join(dir, "key") {
			t.Errorf("Load = %+v, %v", c, err)
		}
	})
	t.Run("without XDG_STATE_HOME the database is under HOME", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		for _, unset := range []func(){func() { unsetenv(t, "XDG_STATE_HOME") }, func() { t.Setenv("XDG_STATE_HOME", "relative") }} {
			unset()
			c, err := config.Load(file)
			if want := filepath.Join(home, ".local", "state", "agent-fox", "agent-fox.db"); err != nil || c.Database.Path != want {
				t.Errorf("database.path = %q, %v; want %q", c.Database.Path, err, want)
			}
		}
	})
	t.Run("without XDG_STATE_HOME or HOME the database has no default", func(t *testing.T) {
		unsetenv(t, "XDG_STATE_HOME")
		unsetenv(t, "HOME")
		if _, err := config.Load(file); err == nil || !strings.Contains(err.Error(), "database.path") {
			t.Errorf("error = %v, want one naming database.path", err)
		}
	})
}

func TestLoad_TakesTheValuesGiven(t *testing.T) {
	t.Setenv("HUBHOST", "hub.example.com")
	t.Setenv("KEYDIR", "/k")
	t.Setenv("PORT", "8091")
	t.Setenv("AF_RETAIN", "48h")
	c := mustLoad(t, `[server]
listen = "127.0.0.1:${PORT}"
[hub]
url = "https://${HUBHOST}/prefix/"
auth_cache_ttl = "15m"
[database]
path = "/data/$notvar/x$"
retain = "${AF_RETAIN}"
[workers]
count = 64
[secrets]
key_file = "${KEYDIR}/key"
`)
	want := config.Config{
		Path:     c.Path,
		Server:   config.Server{Listen: "127.0.0.1:8091"},
		Hub:      config.Hub{URL: "https://hub.example.com/prefix", AuthCacheTTL: 15 * time.Minute},
		Database: config.Database{Path: "/data/$notvar/x$", Retain: 48 * time.Hour},
		Workers:  config.Workers{Count: 64},
		Secrets:  config.Secrets{KeyFile: "/k/key"},
	}
	if c != want {
		t.Errorf("Load = %+v\nwant %+v", c, want)
	}
}

func TestLoad_AcceptsTheBoundsOfTheRanges(t *testing.T) {
	const key = "[secrets]\nkey_file = \"key\"\n"
	hub := func(ttl string) string {
		return "[hub]\nurl = \"http://example.com\"\nauth_cache_ttl = \"" + ttl + "\"\n" + key
	}
	for _, tc := range []struct {
		body string
		ok   func(config.Config) bool
	}{
		{hub("1s"), func(c config.Config) bool { return c.Hub.AuthCacheTTL == time.Second }},
		{hub("90s"), func(c config.Config) bool { return c.Hub.AuthCacheTTL == 90*time.Second }},
		{hub("1s") + "[workers]\ncount = 1\n", func(c config.Config) bool { return c.Workers.Count == 1 }},
		{hub("1s") + "[database]\nretain = \"1h\"\n", func(c config.Config) bool { return c.Database.Retain == time.Hour }},
		{hub("1s") + "[database]\nretain = \"8760h\"\n", func(c config.Config) bool { return c.Database.Retain == 8760*time.Hour }},
		{hub("1s") + "[server]\nlisten = \"127.0.0.1:0\"\n", func(c config.Config) bool { return c.Server.Listen == "127.0.0.1:0" }},
	} {
		if c := mustLoad(t, tc.body); !tc.ok(c) {
			t.Errorf("%q loaded as %+v", tc.body, c)
		}
	}
}

func TestLoad_RefusesAWrongValueNamingTheKey(t *testing.T) {
	const key = "[secrets]\nkey_file = \"key\"\n"
	const hub = "[hub]\nurl = \"http://example.com\"\n"
	ttl := func(v string) string {
		return "[hub]\nurl = \"http://example.com\"\nauth_cache_ttl = " + v + "\n" + key
	}
	unsetenv(t, "AF_NO_SUCH_VARIABLE")
	t.Setenv("AF_EMPTY", "")
	for _, tc := range []struct{ name, body, key string }{
		{"hub.url missing", key, "hub.url"},
		{"hub.url empty", "[hub]\nurl = \"\"\n" + key, "hub.url"},
		{"hub.url relative", "[hub]\nurl = \"hub.example.com\"\n" + key, "hub.url"},
		{"hub.url without a host", "[hub]\nurl = \"http://\"\n" + key, "hub.url"},
		{"hub.url ftp", "[hub]\nurl = \"ftp://x.example.com\"\n" + key, "hub.url"},
		{"hub.url with a query", "[hub]\nurl = \"http://example.com/p?\"\n" + key, "hub.url"},
		{"hub.url with a fragment", "[hub]\nurl = \"http://example.com/#frag\"\n" + key, "hub.url"},
		{"hub.url of the wrong type", "[hub]\nurl = 5\n" + key, "hub.url"},
		{"hub.url an unset variable", "[hub]\nurl = \"${AF_NO_SUCH_VARIABLE}\"\n" + key, "AF_NO_SUCH_VARIABLE"},
		{"auth_cache_ttl under 1s", ttl(`"500ms"`), "hub.auth_cache_ttl"},
		{"auth_cache_ttl over 15m", ttl(`"15m1ns"`), "hub.auth_cache_ttl"},
		{"auth_cache_ttl not a duration", ttl(`"x"`), "hub.auth_cache_ttl"},
		{"auth_cache_ttl of the wrong type", ttl("60"), "hub.auth_cache_ttl"},
		{"auth_cache_ttl an unset variable", ttl(`"${AF_NO_SUCH_VARIABLE}"`), "hub.auth_cache_ttl"},
		{"secrets.key_file missing", hub, "secrets.key_file"},
		{"secrets.key_file empty", hub + "[secrets]\nkey_file = \"\"\n", "secrets.key_file"},
		{"secrets.key_file an empty variable", hub + "[secrets]\nkey_file = \"${AF_EMPTY}\"\n", "secrets.key_file: is required"},
		{"server.listen without a port", hub + key + "[server]\nlisten = \"nocolon\"\n", "server.listen"},
		{"server.listen of the wrong type", hub + key + "[server]\nlisten = true\n", "server.listen"},
		{"server.listen an unset variable", hub + key + "[server]\nlisten = \"${AF_NO_SUCH_VARIABLE}\"\n", "server.listen"},
		{"database.path empty", hub + key + "[database]\npath = \"\"\n", "database.path"},
		{"database.path an unset variable", hub + key + "[database]\npath = \"/x/${AF_NO_SUCH_VARIABLE}\"\n", "database.path: the environment variable AF_NO_SUCH_VARIABLE"},
		{"workers.count 0", hub + key + "[workers]\ncount = 0\n", "workers.count"},
		{"workers.count 65", hub + key + "[workers]\ncount = 65\n", "workers.count"},
		{"workers.count a string", hub + key + "[workers]\ncount = \"two\"\n", "workers.count"},
		{"workers.count a fraction", hub + key + "[workers]\ncount = 2.5\n", "workers.count"},
		{"database.retain 30m", hub + key + "[database]\nretain = \"30m\"\n", "database.retain"},
		{"database.retain 8761h", hub + key + "[database]\nretain = \"8761h\"\n", "database.retain"},
		{"database.retain in days", hub + key + "[database]\nretain = \"30d\"\n", "database.retain"},
		{"database.retain a number", hub + key + "[database]\nretain = 720\n", "database.retain"},
		{"database.retain an unset variable", hub + key + "[database]\nretain = \"${AF_NO_SUCH_VARIABLE}\"\n", "database.retain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.key) || strings.Contains(err.Error(), "unknown key") {
				t.Errorf("error = %v, want a refusal of the value that names %q", err, tc.key)
			}
		})
	}
}

func TestLoad_RefusesAnUnknownKeyNamingIt(t *testing.T) {
	for _, tc := range []struct{ body, key string }{
		{"[server]\nexternal_url = \"http://x.example.com\"\n", "server.external_url"},
		{"[workers]\ncount = 2\nlimit = 3\n", "workers.limit"},
		{"[extra]\n", "unknown key extra"},
		{"[extra]\nanswer = 42\n", "unknown key extra.answer"}, // the key, and not its table as well
	} {
		if _, err := load(t, minimal("http://example.com")+tc.body); err == nil || !strings.HasSuffix(err.Error(), tc.key) {
			t.Errorf("%q: error = %v, want it to end naming %s", tc.body, err, tc.key)
		}
	}
}

func TestLoad_ARefusedHubURLDoesNotRepeatItsCredential(t *testing.T) {
	for _, url := range []string{"http://user:hunter2@example.com/?q=1", "ftp://user:hunter2@example.com", "//user:hunter2@example.com"} {
		if _, err := load(t, minimal(url)); err == nil || !strings.Contains(err.Error(), "hub.url") || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: error = %v, want a refusal naming hub.url without the credential", url, err)
		}
	}
}

func TestLoad_RefusesAFileThatIsNotTOMLNamingThePosition(t *testing.T) {
	for _, body := range []string{"[hub]\nurl = \"http://example.com\"\nsecrets = = 3\n", "\x00\x01\x02 not toml at all"} {
		path := testutil.WriteFile(t, t.TempDir(), "config.toml", body)
		if _, err := config.Load(path); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "line ") {
			t.Errorf("error = %v, want the file and the line", err)
		}
	}
}

func TestHubInClear_IsPlainHTTPToAHostThatIsNotLoopback(t *testing.T) {
	for url, want := range map[string]bool{
		"http://hub.example.internal:8080": true,
		"http://192.0.2.5":                 true,
		"http://127.0.0.1:1":               false,
		"http://localhost:8080":            false,
		"http://hub.localhost":             false,
		"http://[::1]:8080":                false,
		"https://hub.example.com":          false,
	} {
		if got := mustLoad(t, minimal(url)).HubInClear(); got != want {
			t.Errorf("HubInClear for %s = %v, want %v", url, got, want)
		}
	}
	if (config.Config{Hub: config.Hub{URL: "http://%zz"}}).HubInClear() {
		t.Error("a URL that does not parse is in clear")
	}
}
