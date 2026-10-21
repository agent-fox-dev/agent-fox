package docs

import (
	"strings"
	"testing"
)

// TestTheDocumentsNameWhatTheBoxesAndTheClaudeBrainShip_TS_10_65 verifies TS-10-65: after
// make doc, the documents say what this scope ships, in the words its surfaces use.
// Verifies: 10-REQ-10.5
func TestTheDocumentsNameWhatTheBoxesAndTheClaudeBrainShip_TS_10_65(t *testing.T) {
	for file, terms := range map[string][]string{
		"docs/cli.md": {
			// --sandbox: the values, the default, and the failure that names the way out.
			"`landlock`, `podman`, `local` or `none`", "`AF_SANDBOX`", "`landlock` on Linux and `podman` on any other operating system", "does not fall back to `local` or `none`", "`--sandbox` and `AF_SANDBOX`",
			// the flag, the brain, the warnings and the result.
			"`--borrowed-hands`", "`--brain claude`", "#### The claude brain", "#### The providers",
			"| `foreign_commits_reset` |", "| `box_af_version_mismatch` |", "| `brain_usage_unavailable` |", "| `budget_exceeded` |", "| `box_unconfined` |",
			"the hook is the only guard", "`box` | `{provider, image, network}`",
			"box_open {provider, image, network, placement}", "claude_result",
			"| `brain_output_unreadable` |", "`submission.rejected.<attempt>.json`", "agentkit-sandboxd", "on the engine's command line on the host",
		},
		"docs/configuration.md": {
			"`AF_SANDBOX`", "`AF_SANDBOX_IMAGE`", "quay.io/agentfox/tools:latest", "Which credential enters which box", "`--model`", "No forge token",
			// the network of each placement
			"| Where the brain runs | The box holds | Network |", "| on the host (`scripted`", "| in the box (`claude` under `local`", "| at the checkout (`claude` under `none`)", "command line of the engine",
		},
		"docs/model-usage.md": {
			"`claude`", "`reported`", "`--model`", "**after the run**", "`budget_exceeded`", "`AF_SANDBOX`", "`AF_SANDBOX_IMAGE`", "Podman",
			"such a brain has the whole network", "a brain on the host whose brief has no tool that", "`ANTHROPIC_API_KEY` and nothing\nelse",
		},
		"docs/architecture.md": {
			"Placement, as built", "| `claude` | box | box | checkout |", "The `Spec` of a box, for each brain", "The host settings, for each brain", "The tools image.", "containers/tools/Containerfile",
		},
		"docs/development.md": {
			"AF_TEST_LANDLOCK", "AF_TEST_PODMAN", "AF_TEST_CLAUDE", "The fake claude", "`hook_payloads`", "`submits`", "`commands`", "`output_json`", "`.agent-fox/fake.json`",
		},
		"README.md": {"containers/tools/", "quay.io/agentfox/tools"},
	} {
		text := read(t, file)
		for _, term := range terms {
			if !strings.Contains(text, term) {
				t.Errorf("%s does not contain %q", file, term)
			}
		}
	}

	// The sections that this scope settles are no longer left to be written.
	arch := read(t, "docs/architecture.md")
	for _, stale := range []string{
		"- The host settings `af run start` writes for each brain.",
		"- The `Spec` `af run start` builds for each brain",
		"belong to the scope that adds\nthe other box providers",
	} {
		if strings.Contains(arch, stale) {
			t.Errorf("docs/architecture.md still says %q", stale)
		}
	}
	// The one provider and the two brains of the earlier scope are gone from the documents.
	for file, stale := range map[string][]string{
		"README.md":             {"One provider, `local`", "two brains: `scripted`"},
		"docs/model-usage.md":   {"There are two brains", "the only one in this release"},
		"docs/configuration.md": {"the only one in\nthis release"},
	} {
		text := read(t, file)
		for _, s := range stale {
			if strings.Contains(text, s) {
				t.Errorf("%s still says %q", file, s)
			}
		}
	}

	// make doc has written the package docs of the containers.
	if text := read(t, "docs/generated/containers.txt"); !strings.Contains(text, "package containers") {
		t.Errorf("docs/generated/containers.txt = %q, want the package docs of containers", text)
	}
}
