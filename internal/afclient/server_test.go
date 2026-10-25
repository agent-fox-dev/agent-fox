package afclient

import (
	"errors"
	"strings"
	"testing"
)

func TestParseServer_TakesTheFlagOverTheVariable(t *testing.T) {
	for _, c := range []struct {
		name      string
		flag      string
		flagGiven bool
		env       string
		want      Server // URL and Source; FromFlag follows the source
		missing   bool
	}{
		{"both", "http://127.0.0.1:1", true, "http://127.0.0.1:2", Server{URL: "http://127.0.0.1:1", Source: "--server"}, false},
		{"the variable", "", false, "http://127.0.0.1:2", Server{URL: "http://127.0.0.1:2", Source: "AF_URL"}, false},
		{"neither, or an empty variable", "", false, "", Server{}, true},
		{"an empty flag that was given refuses and does not fall back", "", true, "http://127.0.0.1:2", Server{Source: "--server"}, false},
	} {
		s, err := ParseServer(c.flag, c.flagGiven, c.env)
		var se *ServerError
		switch {
		case c.missing:
			if !errors.As(err, &se) || !se.Missing || !strings.Contains(se.Error(), "--server") || !strings.Contains(se.Error(), "AF_URL") {
				t.Errorf("%s: %v, want a missing server that names --server and AF_URL", c.name, err)
			}
		case c.want.URL == "":
			if !errors.As(err, &se) || se.Missing || se.Source != c.want.Source {
				t.Errorf("%s: %v, want a refusal by %s", c.name, err, c.want.Source)
			}
		case err != nil || s.URL != c.want.URL || s.Source != c.want.Source || s.FromFlag != (c.want.Source == SourceFlag):
			t.Errorf("%s: %+v, %v, want %+v", c.name, s, err, c.want)
		}
	}
}

func TestParseServer_KeepsAPathPrefixAndDropsOneTrailingSlash(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com":           "https://example.com",
		"https://example.com/":          "https://example.com",
		"https://example.com:8443/af/":  "https://example.com:8443/af",
		"http://127.0.0.1:9/a/b/":       "http://127.0.0.1:9/a/b",
		"HTTPS://Example.com/Prefix/v1": "https://Example.com/Prefix/v1",
		"http://h/a%20b":                "http://h/a%20b",
	} {
		if s, err := ParseServer(in, true, ""); err != nil || s.URL != want {
			t.Errorf("ParseServer(%q) = %q, %v, want %q", in, s.URL, err, want)
		}
	}
}

func TestParseServer_RefusesAMalformedURLNamingItsSourceAndRule(t *testing.T) {
	for _, c := range []struct{ in, rule string }{
		{"localhost:80", "must be an absolute http or https URL"},
		{"ftp://h", "must be an absolute http or https URL"},
		{"//h/path", "must be an absolute http or https URL"},
		{"/api", "must be an absolute http or https URL"},
		{"http://h:notaport", "must be an absolute http or https URL"},
		{"http://", "must name a host"},
		{"http:///path", "must name a host"},
		{"http://h/?q=1", "must not carry a query"},
		{"http://h/?", "must not carry a query"},
		{"http://h/#f", "must not carry a fragment"},
		{"http://h/#", "must not carry a fragment"},
		{"http://u:hunter2pw@h", "must not carry user info"},
		{"http://u@h", "must not carry user info"},
	} {
		for source, parse := range map[string]func(string) (Server, error){
			"--server": func(u string) (Server, error) { return ParseServer(u, true, "") },
			"AF_URL":   func(u string) (Server, error) { return ParseServer("", false, u) },
		} {
			_, err := parse(c.in)
			var se *ServerError
			if !errors.As(err, &se) || se.Missing || se.Source != source {
				t.Errorf("%q by %s: %v, want a refusal", c.in, source, err)
				continue
			}
			if want := "the server URL given by " + source + " " + c.rule; !strings.HasPrefix(se.Error(), want) || strings.Contains(se.Error(), "hunter2pw") {
				t.Errorf("%q by %s: %q, want %q... and no password", c.in, source, se.Error(), want)
			}
		}
	}
}

func TestInsecure_IsPlainHTTPToAHostThatIsNotLoopback(t *testing.T) {
	for in, want := range map[string]bool{
		"http://example.com":     true,
		"http://10.0.0.1:8090":   true,
		"http://[2001:db8::1]:1": true,
		"http://localhost:1":     false,
		"http://LOCALHOST":       false,
		"http://127.1.2.3:9":     false,
		"http://[::1]:1":         false,
		"https://example.com":    false,
	} {
		s, err := ParseServer(in, true, "")
		if err != nil || s.Insecure() != want {
			t.Errorf("Insecure(%q) = %v (%v), want %v", in, s.Insecure(), err, want)
		}
	}
	if (Server{URL: "http://example.com"}).Insecure() {
		t.Error("a Server that ParseServer did not make is insecure")
	}
}
