package main

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The roots the rule covers: the tests of the binary, the hub client and the
// service packages, and the fake hub, which is not a test file but is test support.
var scannedRoots = []string{"../../cmd/agent-fox", "../../internal/hub", "../../internal/service"}

// inScope reports whether the rule covers the file: every test file, and every file
// of the fake hub.
func inScope(path string) bool {
	return strings.HasSuffix(path, "_test.go") || filepath.Base(filepath.Dir(path)) == "fakehub"
}

// inertTLDs end the names of hosts that are reserved for examples and for
// documentation (RFC 2606, RFC 6761 and the private-use TLD .internal), and
// inertDomains are the second-level domains reserved by RFC 2606: a test may write
// them as strings that are parsed or configured, never resolved or dialled.
var (
	inertTLDs    = []string{".invalid", ".example", ".test", ".internal", ".localhost"}
	inertDomains = []string{"example.com", "example.org", "example.net"}
)

// documentationNets are the IPv4 prefixes that RFC 5737 reserves for documentation.
var documentationNets = []string{"192.0.2.", "198.51.100.", "203.0.113."}

// inertHost reports whether a test may name the host: the loopback interface, or
// a name that is reserved for examples.
func inertHost(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
		return true
	}
	for _, tld := range inertTLDs {
		if strings.HasSuffix(host, tld) {
			return true
		}
	}
	for _, domain := range inertDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	for _, prefix := range documentationNets {
		if strings.HasPrefix(host, prefix) {
			return true
		}
	}
	return false
}

var (
	urlPattern    = regexp.MustCompile(`https?://(\[[0-9a-fA-F:]+\]|[A-Za-z0-9._-]+)`)
	ipPortPattern = regexp.MustCompile(`"(\d+\.\d+\.\d+\.\d+):\d+"`)
	dialPattern   = regexp.MustCompile(`net\.(?:Dial|DialTimeout|Listen)\([^,]+,\s*"([^"]+)"`)
	directCalls   = regexp.MustCompile(`\bhttp\.(?:Get|Post|Head|PostForm)\(|\bnet\.LookupHost\(|\bnet\.LookupIP\(`)
	keyFromEnv    = regexp.MustCompile(`AF_KEY|Getenv\("[A-Za-z_]*KEY[A-Za-z_]*"\)`)
	keyPathInHome = regexp.MustCompile(`key_file"?\s*[=:]\s*q?\(?"(/|~)`)
)

// TestTheServiceTestsUseOnlyLoopbackNetworkingAndNoOutsideKey_TS_11_78 verifies TS-11-78:
// The service tests use only loopback networking and need no real hub or
// pre-existing key. The scan reads the tests of the binary, the hub client and the
// service packages, and the fake hub; make check is the gate that runs them offline.
// Verifies: 11-REQ-10.8
func TestTheServiceTestsUseOnlyLoopbackNetworkingAndNoOutsideKey_TS_11_78(t *testing.T) {
	var files []string
	for _, root := range scannedRoots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			if inScope(path) && filepath.Base(path) != "scan_test.go" { // this file states the rules
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// The scan must have found the files it is for.
	for _, want := range []string{
		"cmd/agent-fox/start_test.go", "cmd/agent-fox/smoke_test.go", "internal/hub/fakehub/fakehub.go", "internal/hub/client_test.go",
		"internal/service/api/workspaces_test.go", "internal/service/config/config_test.go", "internal/service/store/store_test.go",
	} {
		if !slices.ContainsFunc(files, func(f string) bool { return strings.HasSuffix(filepath.ToSlash(f), want) }) {
			t.Errorf("the scan did not find %s among %d files", want, len(files))
		}
	}
	if slices.ContainsFunc(files, func(f string) bool { return strings.HasSuffix(f, "internal/hub/client.go") }) {
		t.Error("the scan reads the production client")
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			at := fmt.Sprintf("%s:%d", file, i+1)
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, m := range urlPattern.FindAllStringSubmatch(line, -1) {
				if !inertHost(m[1]) {
					t.Errorf("%s names the URL %s, whose host is neither the loopback interface nor a name reserved for examples", at, m[0])
				}
			}
			for _, m := range ipPortPattern.FindAllStringSubmatch(line, -1) {
				if !inertHost(m[1]) {
					t.Errorf("%s names the address %s, which is not on the loopback interface or reserved for documentation", at, m[1])
				}
			}
			for _, m := range dialPattern.FindAllStringSubmatch(line, -1) {
				host, _, err := net.SplitHostPort(m[1])
				if err != nil || !inertHost(host) {
					t.Errorf("%s dials or listens on %q, want the loopback interface", at, m[1])
				}
			}
			if directCalls.MatchString(line) {
				t.Errorf("%s calls the network directly: %s", at, strings.TrimSpace(line))
			}
			if keyFromEnv.MatchString(line) {
				t.Errorf("%s reads a key from the environment: %s", at, strings.TrimSpace(line))
			}
			if keyPathInHome.MatchString(line) {
				t.Errorf("%s names a key file outside the test's own directory: %s", at, strings.TrimSpace(line))
			}
		}
	}
}

// TestTheScanRecognisesWhatItIsMeantToRefuse checks the rules of the scan on
// strings, so that it cannot pass by looking at nothing.
// Verifies: 11-REQ-10.8
func TestTheScanRecognisesWhatItIsMeantToRefuse(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "localhost": true, "[::1]": true, "::1": true, "hub.example.com": true, "example.org": true,
		"hub.example.internal": true, "no-such-host.invalid": true, "192.0.2.1": true, "203.0.113.9": true, "a.test": true,
		"github.com": false, "api.github.com": false, "8.8.8.8": false, "10.0.0.5": false, "hub.local": false,
		"notexample.com.evil.io": false, "evil-example.com": false, "badexample.com": false,
	} {
		if got := inertHost(host); got != want {
			t.Errorf("inertHost(%q) = %v, want %v", host, got, want)
		}
	}
	for line, rx := range map[string]*regexp.Regexp{
		`http.Get("http://127.0.0.1/")`:                  directCalls,
		`net.Dial("tcp", "8.8.8.8:53")`:                  dialPattern,
		`os.Getenv("AF_KEY_FILE")`:                       keyFromEnv,
		`s.Doc["secrets"] = {"key_file": q("/etc/key")}`: keyPathInHome,
		`"10.1.2.3:80"`:                                  ipPortPattern,
		`"https://api.github.com/user"`:                  urlPattern,
	} {
		if !rx.MatchString(line) {
			t.Errorf("%v does not match %q", rx, line)
		}
	}
}
