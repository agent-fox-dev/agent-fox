package afclient

import (
	"errors"
	"strings"
	"testing"
)

// TestParseServerTakesTheFlagOverTheVariableAndTreatsAnEmptyVariableAsUnset_TS_16_17
// verifies TS-16-17 for the parsing: the flag wins, an empty AF_URL is unset, and an empty flag
// that was given is a refusal and not a fall back to the variable.
// Verifies: 16-REQ-3.1
func TestParseServerTakesTheFlagOverTheVariableAndTreatsAnEmptyVariableAsUnset_TS_16_17(t *testing.T) {
	s, err := ParseServer("http://127.0.0.1:1", true, "http://127.0.0.1:2")
	if err != nil || s.URL != "http://127.0.0.1:1" || s.Source != "--server" || !s.FromFlag {
		t.Errorf("flag and variable: %+v, %v, want the flag", s, err)
	}
	s, err = ParseServer("", false, "http://127.0.0.1:2")
	if err != nil || s.URL != "http://127.0.0.1:2" || s.Source != "AF_URL" || s.FromFlag {
		t.Errorf("variable only: %+v, %v, want the variable", s, err)
	}
	var missing *ServerError
	if _, err = ParseServer("", false, ""); !errors.As(err, &missing) || !missing.Missing {
		t.Errorf("neither: %v, want a missing-server error", err)
	}
	var bad *ServerError
	if _, err = ParseServer("", true, "http://127.0.0.1:2"); !errors.As(err, &bad) || bad.Missing || bad.Source != "--server" {
		t.Errorf("an empty flag that was given: %v, want a refusal that names --server", err)
	}
}

// TestParseServerAcceptsHTTPSAndAPathPrefixAndDropsOneTrailingSlash_TS_16_18 verifies
// TS-16-18 for the parsing.
// Verifies: 16-REQ-3.2
func TestParseServerAcceptsHTTPSAndAPathPrefixAndDropsOneTrailingSlash_TS_16_18(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com":           "https://example.com",
		"https://example.com/":          "https://example.com",
		"https://example.com:8443/af/":  "https://example.com:8443/af",
		"http://127.0.0.1:9/prefix":     "http://127.0.0.1:9/prefix",
		"http://127.0.0.1:9/a/b/":       "http://127.0.0.1:9/a/b",
		"HTTPS://Example.com/Prefix/v1": "https://Example.com/Prefix/v1",
	} {
		s, err := ParseServer(in, true, "")
		if err != nil || s.URL != want {
			t.Errorf("ParseServer(%q) = %q, %v, want %q", in, s.URL, err, want)
		}
	}
}

// TestParseServerRefusesWhatIsNotAnAbsoluteHTTPOrHTTPSURLWithAHostAndNeverEchoesUserInfo_TS_16_19
// verifies TS-16-19 for the parsing.
// Verifies: 16-REQ-3.3
func TestParseServerRefusesWhatIsNotAnAbsoluteHTTPOrHTTPSURLWithAHostAndNeverEchoesUserInfo_TS_16_19(t *testing.T) {
	for _, c := range []struct{ in, rule string }{
		{"localhost:80", "http or https"},
		{"ftp://h", "http or https"},
		{"//h/path", "http or https"},
		{"/api", "http or https"},
		{"http://", "host"},
		{"http:///path", "host"},
		{"http://h/?q=1", "query"},
		{"http://h/?", "query"},
		{"http://h/#f", "fragment"},
		{"http://h/#", "fragment"},
		{"http://u:hunter2pw@h", "user info"},
		{"http://u@h", "user info"},
		{"http://h:notaport", "http or https"},
	} {
		for _, flagGiven := range []bool{true, false} {
			var s Server
			var err error
			if flagGiven {
				s, err = ParseServer(c.in, true, "")
			} else {
				s, err = ParseServer("", false, c.in)
			}
			var se *ServerError
			if !errors.As(err, &se) || se.Missing {
				t.Errorf("ParseServer(%q) = %+v, %v, want a refusal", c.in, s, err)
				continue
			}
			wantSource := "AF_URL"
			if flagGiven {
				wantSource = "--server"
			}
			if se.Source != wantSource || !strings.Contains(se.Error(), wantSource) || !strings.Contains(se.Error(), c.rule) {
				t.Errorf("ParseServer(%q): %q (source %q), want it to name %s and %q", c.in, se.Error(), se.Source, wantSource, c.rule)
			}
			if strings.Contains(se.Error(), "hunter2pw") {
				t.Errorf("ParseServer(%q) echoes the password: %q", c.in, se.Error())
			}
		}
	}
}

// TestInsecureIsPlainHTTPToAHostThatIsNotLoopback_TS_16_21 verifies TS-16-21 for the
// decision: localhost, 127.0.0.0/8 and ::1 are loopback.
// Verifies: 16-REQ-3.5
func TestInsecureIsPlainHTTPToAHostThatIsNotLoopback_TS_16_21(t *testing.T) {
	for in, want := range map[string]bool{
		"http://example.com":     true,
		"http://10.0.0.1:8090":   true,
		"http://[2001:db8::1]:1": true,
		"http://localhost:1":     false,
		"http://LOCALHOST":       false,
		"http://127.0.0.1":       false,
		"http://127.1.2.3:9":     false,
		"http://[::1]:1":         false,
		"https://example.com":    false,
		"https://127.0.0.1":      false,
	} {
		s, err := ParseServer(in, true, "")
		if err != nil {
			t.Errorf("ParseServer(%q): %v", in, err)
			continue
		}
		if got := s.Insecure(); got != want {
			t.Errorf("Insecure(%q) = %v, want %v", in, got, want)
		}
	}
}
