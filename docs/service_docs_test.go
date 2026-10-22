package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// lacking returns the terms that the text does not contain.
func lacking(text string, terms ...string) []string {
	var out []string
	for _, term := range terms {
		if !strings.Contains(text, term) {
			out = append(out, term)
		}
	}
	return out
}

// sectionFrom is the text from the heading to the end of the document.
func sectionFrom(t *testing.T, doc, heading string) string {
	t.Helper()
	i := strings.Index(doc, heading)
	if i < 0 {
		t.Fatalf("there is no heading %q", heading)
	}
	return doc[i:]
}

// TestAPIMdNamesTheRoutesTheCodesAndTheRules_TS_11_73 verifies TS-11-73:
// docs/api.md exists and names every route, error code, the 201/200 rule, the null
// token_user and what label_prefix means.
// Verifies: 11-REQ-10.3
func TestAPIMdNamesTheRoutesTheCodesAndTheRules_TS_11_73(t *testing.T) {
	doc := read(t, "docs/api.md")

	for _, route := range []string{
		"GET /healthz", "GET /readyz", "GET /version",
		"GET /api/v1/workspaces", "GET /api/v1/workspaces/{slug}", "PUT /api/v1/workspaces/{slug}", "DELETE /api/v1/workspaces/{slug}",
	} {
		if !strings.Contains(doc, route) {
			t.Errorf("docs/api.md does not name the route %s", route)
		}
	}

	// every error code, with its status, in a row of a table
	for code, status := range map[string]int{
		"invalid_request": 400, "unauthenticated": 401, "not_found": 404, "method_not_allowed": 405,
		"payload_too_large": 413, "unsupported_media_type": 415, "invalid_hub_token": 422,
		"internal": 500, "hub_unavailable": 502, "not_ready": 503,
	} {
		row := regexp.MustCompile(fmt.Sprintf("\\|\\s*`%s`\\s*\\|\\s*%d\\s*\\|", code, status))
		if !row.MatchString(doc) {
			t.Errorf("docs/api.md has no table row giving %s the status %d", code, status)
		}
	}

	for _, term := range []string{
		"Bearer", "error_type", "given at subscribe", "null",
		"201", "200", "token_state", "`ok`", "`unreadable`",
		"subscribed_by", "token_user", "label_prefix", "created_at", "updated_at",
		"hub_token", "WWW-Authenticate", "64 KiB", "application/json",
	} {
		if !strings.Contains(doc, term) {
			t.Errorf("docs/api.md does not contain %q", term)
		}
	}
	for _, rule := range []string{"on create", "on replace"} {
		if !strings.Contains(doc, rule) {
			t.Errorf("docs/api.md does not say %q", rule)
		}
	}
}

// TestCLIMdHasAnAgentFoxSectionAndKeepsAf_TS_11_74 verifies TS-11-74:
// docs/cli.md has an agent-fox section with flags, exit codes and lookup order.
// Verifies: 11-REQ-10.4
func TestCLIMdHasAnAgentFoxSectionAndKeepsAf_TS_11_74(t *testing.T) {
	doc := read(t, "docs/cli.md")
	if missing := lacking(doc, "agent-fox", "--config", "--version", "--help", "./config.toml", "$XDG_CONFIG_HOME/agent-fox/config.toml"); len(missing) > 0 {
		t.Errorf("docs/cli.md does not mention %q", missing)
	}
	section := sectionFrom(t, doc, "## The agent-fox command line")
	for _, code := range []int{0, 1, 2} {
		row := regexp.MustCompile(fmt.Sprintf("\\|\\s*`%d`\\s*\\|", code))
		if !row.MatchString(section) {
			t.Errorf("the agent-fox section has no table row for the exit code %d", code)
		}
	}
	// What was there is still there.
	if missing := lacking(doc, "# The af command line", "### triage", "### fix", "## The envelope", "## Statuses and exit codes"); len(missing) > 0 {
		t.Errorf("docs/cli.md lost %q", missing)
	}
	if strings.Contains(doc, "`af` is the one binary of agent-fox") {
		t.Error("docs/cli.md still says that af is the one binary of agent-fox")
	}
}

// TestConfigurationMdListsTheKeysAndTheKeyRecipe_TS_11_75 verifies TS-11-75:
// docs/configuration.md documents the keys, the unsupported keys and the umask 077
// key recipe.
// Verifies: 11-REQ-10.5
func TestConfigurationMdListsTheKeysAndTheKeyRecipe_TS_11_75(t *testing.T) {
	doc := read(t, "docs/configuration.md")
	if missing := lacking(doc,
		"server.listen", "hub.url", "hub.auth_cache_ttl", "database.path", "secrets.key_file",
		"(umask 077; openssl rand -base64 32 > key)", "server.external_url", "[workers]", "database.retain",
		"127.0.0.1:8090", "60s", "15m", "${NAME}", "XDG_STATE_HOME",
	); len(missing) > 0 {
		t.Errorf("docs/configuration.md does not contain %q", missing)
	}
	section := sectionFrom(t, doc, "## The agent-fox service")
	for _, sentence := range []string{"not accepted in this release"} {
		if !strings.Contains(section, sentence) {
			t.Errorf("the agent-fox section does not say %q", sentence)
		}
	}
	// The sample of the PRD is not shown as a configuration that works.
	for _, stale := range []string{"external_url = "} {
		if strings.Contains(section, stale) {
			t.Errorf("the agent-fox section shows %q, a key that is refused", stale)
		}
	}
	// What was there is still there.
	if missing := lacking(doc, "GITHUB_TOKEN", ".agent-fox", "### gate.json"); len(missing) > 0 {
		t.Errorf("docs/configuration.md lost %q", missing)
	}
}

// TestTheOtherDocumentsDescribeTheService_TS_11_76 verifies TS-11-76:
// model-usage, architecture, development and README describe the service.
// Verifies: 11-REQ-10.6
func TestTheOtherDocumentsDescribeTheService_TS_11_76(t *testing.T) {
	for file, terms := range map[string][]string{
		"docs/model-usage.md":  {"agent-fox runs no model in this release"},
		"docs/architecture.md": {"The service, as built", "state file", "identity provider", "internal/service/api", "af_schema_migrations"},
		"docs/development.md": {
			"`cmd/agent-fox`", "`internal/hub`", "`internal/hub/fakehub`",
			"`internal/service/config`", "`internal/service/secrets`", "`internal/service/store`", "`internal/service/ttl`", "`internal/service/api`",
		},
		"README.md": {"agent-fox --config", "cmd/agent-fox", "bin/agent-fox", "internal/service/", "internal/hub/"},
	} {
		text := read(t, file)
		if missing := lacking(text, terms...); len(missing) > 0 {
			t.Errorf("%s does not contain %q", file, missing)
		}
	}
	// The statements that the service makes untrue are gone.
	if text := read(t, "docs/development.md"); strings.Contains(text, "make build      # go build ./..., and bin/af\n") {
		t.Error("docs/development.md still says that make build writes only bin/af")
	}
	if text := read(t, "README.md"); strings.Contains(text, "make build          # go build ./..., and bin/af\n") {
		t.Error("README.md still says that make build writes only bin/af")
	}
}

// TestTheGeneratedDocsHoldTheServicesPackageDocs_TS_11_77 verifies TS-11-77:
// The generated docs include docs/generated/cmd/agent-fox.txt.
// Verifies: 11-REQ-10.7
func TestTheGeneratedDocsHoldTheServicesPackageDocs_TS_11_77(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("generated", "cmd", "agent-fox.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// go doc -all, which make doc runs on the import path, prints no package clause
	// for a command, as in docs/generated/cmd/af.txt: the file starts with the
	// package comment, "Command agent-fox is …".
	if len(data) == 0 || !strings.HasPrefix(string(data), "Command agent-fox ") {
		t.Errorf("docs/generated/cmd/agent-fox.txt = %q, want the package docs of cmd/agent-fox", data)
	}
}
