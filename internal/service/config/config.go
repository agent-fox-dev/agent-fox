// Package config loads and validates the config.toml of the agent-fox service.
//
// The file has seven keys and no others (server.listen, hub.url,
// hub.auth_cache_ttl, database.path, database.retain, workers.count and
// secrets.key_file). A key that is not defined, a value of the wrong type and a
// value out of range are each an error that names the dotted key, and nothing is
// clamped or defaulted in place of a value that was given.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// The defaults, and the ranges of hub.auth_cache_ttl, workers.count and
// database.retain.
const (
	DefaultListen = "127.0.0.1:8090"
	DefaultTTL    = 60 * time.Second
	MinTTL        = time.Second
	MaxTTL        = 15 * time.Minute

	DefaultWorkers = 2
	MinWorkers     = 1
	MaxWorkers     = 64

	DefaultRetain = 720 * time.Hour
	MinRetain     = time.Hour
	MaxRetain     = 8760 * time.Hour
)

// Config is the validated configuration of the service. Every path in it is
// absolute and hub.url has no trailing slash.
type Config struct {
	// Path is the absolute path of the file the configuration was read from.
	Path     string
	Server   Server
	Hub      Hub
	Database Database
	Workers  Workers
	Secrets  Secrets
}

// Server is the [server] table.
type Server struct {
	Listen string
}

// Hub is the [hub] table.
type Hub struct {
	URL          string
	AuthCacheTTL time.Duration
}

// Database is the [database] table.
type Database struct {
	Path string
	// Retain is how long a finished job is kept.
	Retain time.Duration
}

// Workers is the [workers] table.
type Workers struct {
	// Count is the number of attempts in flight across all workspaces.
	Count int
}

// Secrets is the [secrets] table.
type Secrets struct {
	KeyFile string
}

// document is the file as it is decoded: a pointer says that the key was
// given, so that a key left out and a key left empty are told apart.
type document struct {
	Server struct {
		Listen *string `toml:"listen"`
	} `toml:"server"`
	Hub struct {
		URL          *string `toml:"url"`
		AuthCacheTTL *string `toml:"auth_cache_ttl"`
	} `toml:"hub"`
	Database struct {
		Path   *string `toml:"path"`
		Retain *string `toml:"retain"`
	} `toml:"database"`
	Workers struct {
		Count *int `toml:"count"`
	} `toml:"workers"`
	Secrets struct {
		KeyFile *string `toml:"key_file"`
	} `toml:"secrets"`
}

// Load reads the configuration from explicit, or, when that is empty, from the
// first of ./config.toml and $XDG_CONFIG_HOME/agent-fox/config.toml
// ($HOME/.config/agent-fox/config.toml when XDG_CONFIG_HOME is unset) that
// exists. An empty XDG_CONFIG_HOME counts as unset.
func Load(explicit string) (Config, error) {
	path, err := find(explicit)
	if err != nil {
		return Config{}, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("config file %s: %w", path, err)
	}

	var doc document
	md, err := toml.DecodeFile(path, &doc)
	if err != nil {
		return Config{}, decodeError(path, err)
	}
	if keys := unknownKeys(md); len(keys) > 0 {
		return Config{}, fmt.Errorf("config file %s: unknown key %s", path, strings.Join(keys, ", "))
	}
	c, err := doc.resolve(filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("config file %s: %w", path, err)
	}
	c.Path = path
	return c, nil
}

// find is the file to read: explicit, if it is given, else the first of the
// lookup that exists. When none does, the error lists every path tried.
func find(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("config file: %w", err)
		}
		return explicit, nil
	}
	paths := lookupPaths()
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("no config file found; tried %s; name one with --config", strings.Join(paths, ", "))
}

// lookupPaths are the files looked for when --config is not given, in order.
func lookupPaths() []string {
	var paths []string
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(wd, "config.toml"))
	} else {
		paths = append(paths, "./config.toml")
	}
	if dir := xdgDir("XDG_CONFIG_HOME", ".config"); dir != "" {
		paths = append(paths, filepath.Join(dir, "agent-fox", "config.toml"))
	}
	return paths
}

// xdgDir is the directory the XDG variable names, else fallback under the home
// directory; empty when neither is known. A relative XDG path is no XDG path.
func xdgDir(variable, fallback string) string {
	if dir := os.Getenv(variable); filepath.IsAbs(dir) {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, fallback)
	}
	return ""
}

// lastKey picks the key out of the message of a decoding error.
var lastKey = regexp.MustCompile(`\(last key "([^"]+)"\)`)

// decodeError says what is wrong with the file: where a syntax error is, or
// which key holds a value of the wrong type.
func decodeError(path string, err error) error {
	var syntax toml.ParseError
	if errors.As(err, &syntax) {
		return fmt.Errorf("config file %s: line %d, column %d: %s", path, syntax.Position.Line, syntax.Position.Col, syntax.Message)
	}
	if m := lastKey.FindStringSubmatch(err.Error()); m != nil {
		_, detail, _ := strings.Cut(err.Error(), "): ")
		return fmt.Errorf("config file %s: %s: %s", path, m[1], detail)
	}
	return fmt.Errorf("config file %s: %w", path, err)
}

// unknownKeys are the dotted keys of the file that the configuration does not
// define. A table that holds an unknown key is not named as well.
func unknownKeys(md toml.MetaData) []string {
	var keys []string
	for _, k := range md.Undecoded() {
		keys = append(keys, k.String())
	}
	slices.Sort(keys)
	return slices.DeleteFunc(keys, func(k string) bool {
		return slices.ContainsFunc(keys, func(other string) bool { return strings.HasPrefix(other, k+".") })
	})
}

// resolve checks the document, applies the defaults, expands the variables and
// makes the paths absolute against dir, the directory of the file. The first
// key that is wrong is the error.
func (d *document) resolve(dir string) (Config, error) {
	var c Config
	var err error

	listen := DefaultListen
	if d.Server.Listen != nil {
		if listen, err = expand("server.listen", *d.Server.Listen); err != nil {
			return Config{}, err
		}
	}
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return Config{}, fmt.Errorf("server.listen: %q is not a host:port (%v)", listen, trimOp(err))
	}
	c.Server.Listen = listen

	if c.Hub.URL, err = d.hubURL(); err != nil {
		return Config{}, err
	}
	if c.Hub.AuthCacheTTL, err = d.authCacheTTL(); err != nil {
		return Config{}, err
	}

	if d.Database.Path == nil {
		state := xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state"))
		if state == "" {
			return Config{}, errors.New("database.path: has no default, because neither XDG_STATE_HOME nor HOME is set")
		}
		c.Database.Path = filepath.Join(state, "agent-fox", "agent-fox.db")
	} else if c.Database.Path, err = resolvePath("database.path", *d.Database.Path, dir); err != nil {
		return Config{}, err
	}

	if c.Workers.Count, err = d.workersCount(); err != nil {
		return Config{}, err
	}
	if c.Database.Retain, err = d.retain(); err != nil {
		return Config{}, err
	}

	if d.Secrets.KeyFile == nil {
		return Config{}, errors.New("secrets.key_file: is required")
	}
	if c.Secrets.KeyFile, err = resolvePath("secrets.key_file", *d.Secrets.KeyFile, dir); err != nil {
		return Config{}, err
	}
	return c, nil
}

// trimOp drops the "address x: " that net puts in front of its reasons.
func trimOp(err error) string {
	if addr, ok := errors.AsType[*net.AddrError](err); ok {
		return addr.Err
	}
	return err.Error()
}

// hubURL is hub.url, checked and without a trailing slash.
func (d *document) hubURL() (string, error) {
	if d.Hub.URL == nil {
		return "", errors.New("hub.url: is required")
	}
	raw, err := expand("hub.url", *d.Hub.URL)
	if err != nil {
		return "", err
	}
	if raw == "" {
		return "", errors.New("hub.url: is required, and is empty")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("hub.url: is not an absolute http or https URL")
	}
	if strings.ContainsAny(raw, "?#") {
		return "", errors.New("hub.url: must not have a query or a fragment")
	}
	return strings.TrimRight(raw, "/"), nil
}

// authCacheTTL is hub.auth_cache_ttl, which is refused outside MinTTL to MaxTTL
// and never clamped.
func (d *document) authCacheTTL() (time.Duration, error) {
	if d.Hub.AuthCacheTTL == nil {
		return DefaultTTL, nil
	}
	raw, err := expand("hub.auth_cache_ttl", *d.Hub.AuthCacheTTL)
	if err != nil {
		return 0, err
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("hub.auth_cache_ttl: %q is not a duration such as 60s", raw)
	}
	if ttl < MinTTL || ttl > MaxTTL {
		return 0, fmt.Errorf("hub.auth_cache_ttl: %s is outside %s to %s", raw, MinTTL, MaxTTL)
	}
	return ttl, nil
}

// retain is database.retain, which is refused outside MinRetain to MaxRetain and
// never clamped.
func (d *document) retain() (time.Duration, error) {
	if d.Database.Retain == nil {
		return DefaultRetain, nil
	}
	raw, err := expand("database.retain", *d.Database.Retain)
	if err != nil {
		return 0, err
	}
	retain, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("database.retain: %q is not a duration such as 720h", raw)
	}
	if retain < MinRetain || retain > MaxRetain {
		return 0, fmt.Errorf("database.retain: %s is outside %s to %s", raw, MinRetain, MaxRetain)
	}
	return retain, nil
}

// workersCount is workers.count, which is refused outside MinWorkers to
// MaxWorkers and never clamped.
func (d *document) workersCount() (int, error) {
	if d.Workers.Count == nil {
		return DefaultWorkers, nil
	}
	if n := *d.Workers.Count; n < MinWorkers || n > MaxWorkers {
		return 0, fmt.Errorf("workers.count: %d is outside %d to %d", n, MinWorkers, MaxWorkers)
	}
	return *d.Workers.Count, nil
}

// resolvePath is a path of the file: expanded, not empty, and absolute against dir.
func resolvePath(key, value, dir string) (string, error) {
	p, err := expand(key, value)
	if err != nil {
		return "", err
	}
	if p == "" {
		return "", fmt.Errorf("%s: is required, and is empty", key)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p), nil
}

// varRef is a ${NAME} in a value.
var varRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expand replaces every ${NAME} in value with the variable of that name. A
// variable that is set to empty expands to empty; one that is not set is an
// error that names it and the key. Text that is not ${NAME} is left alone, and
// an expanded value is not expanded again.
func expand(key, value string) (string, error) {
	var unset string
	out := varRef.ReplaceAllStringFunc(value, func(ref string) string {
		name := ref[2 : len(ref)-1]
		v, ok := os.LookupEnv(name)
		if !ok && unset == "" {
			unset = name
		}
		return v
	})
	if unset != "" {
		return "", fmt.Errorf("%s: the environment variable %s is not set", key, unset)
	}
	return out, nil
}

// HubInClear reports whether hub.url is plain http to a host that is not a
// loopback address: caller tokens and workspace tokens then cross the link in
// clear.
func (c Config) HubInClear() bool {
	u, err := url.Parse(c.Hub.URL)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}
