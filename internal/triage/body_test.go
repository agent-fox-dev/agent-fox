package triage

import (
	"flag"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update rewrites the golden files: go test ./internal/triage -update.
var update = flag.Bool("update", false, "rewrite the golden files")

const (
	testRun   = "20261020_072649_a31f9c"
	beginMark = "<!-- agent-fox:original-report:begin -->"
	endMark   = "<!-- agent-fox:original-report:end -->"
)

// fullDiagnosis has every section: a path with a line and one without, and a
// question with options.
func fullDiagnosis() Diagnosis {
	return Diagnosis{
		Title:      "Login fails on submit",
		Severity:   "high",
		Confidence: "suspected",
		RootCause:  "The handler dereferences a nil session.\nIt is created lazily.",
		AffectedFiles: []AffectedFile{
			{Path: "auth/login.go", Line: 42, Note: "nil session dereference"},
			{Path: "auth/session.go", Note: "lazy creation"},
		},
		SuggestedFix:       "Create the session before the handler runs.",
		AcceptanceCriteria: []string{"Submitting the login form with valid credentials returns 200", "No panic is logged"},
		Question:           &Question{Text: "Should an expired session log the user out?", Options: []string{"Yes", "No"}},
	}
}

// notReproducible has only what a diagnosis that found nothing must have.
func notReproducible() Diagnosis {
	return Diagnosis{Title: "Crash on submit", Severity: "low", Confidence: "not_reproducible", RootCause: "Could not reproduce with the steps given."}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; create it with go test -update", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// TestRenderIsDeterministicAndMatchesGoldens_TS_08_43 verifies TS-08-43:
// the body renderer is deterministic and matches the goldens for a full and a
// not_reproducible diagnosis.
// Verifies: 08-REQ-7.1
func TestRenderIsDeterministicAndMatchesGoldens_TS_08_43(t *testing.T) {
	full := Render(fullDiagnosis(), testRun, "The login button does nothing.\nIt used to work.\n")
	if again := Render(fullDiagnosis(), testRun, "The login button does nothing.\nIt used to work.\n"); again != full {
		t.Error("two renders of the same diagnosis, run and original differ")
	}
	golden(t, "body_full.md", full)
	golden(t, "body_not_reproducible.md", Render(notReproducible(), testRun, "It crashes sometimes."))

	if first, _, _ := strings.Cut(full, "\n"); first != "<!-- agent-fox:triage v1 run="+testRun+" -->" {
		t.Errorf("the first line is %q, want the triage marker", first)
	}
	for _, want := range []string{"- `auth/login.go:42` — nil session dereference", "- `auth/session.go` — lazy creation", "\nA. Yes\nB. No\n", "- [ ] No panic is logged"} {
		if !strings.Contains(full, want) {
			t.Errorf("the body lacks %q", want)
		}
	}

	bare := Render(notReproducible(), testRun, "x")
	for _, section := range []string{"## Affected files", "## Suggested fix", "## Acceptance Criteria", "## Question"} {
		if strings.Contains(bare, section) {
			t.Errorf("an empty section %s is rendered", section)
		}
	}
	for _, section := range []string{"## Diagnosis", "## Original report"} {
		if !strings.Contains(bare, section) {
			t.Errorf("%s is missing from a body with nothing else", section)
		}
	}
}

// TestRenderDiagnosisOnlyHasNoOriginalSection verifies the comment fallback of
// 08-REQ-8.4 and the size rule of 08-REQ-5.6: the diagnosis alone, without the
// original-report section and its markers.
func TestRenderDiagnosisOnlyHasNoOriginalSection(t *testing.T) {
	d := RenderDiagnosisOnly(fullDiagnosis(), testRun)
	for _, absent := range []string{"## Original report", beginMark, endMark, "<details>"} {
		if strings.Contains(d, absent) {
			t.Errorf("the diagnosis alone contains %q:\n%s", absent, d)
		}
	}
	if !strings.Contains(d, "## Diagnosis") || !strings.Contains(d, "**Severity:** high · **Confidence:** suspected") {
		t.Errorf("the diagnosis alone lacks the diagnosis:\n%s", d)
	}
	full := Render(fullDiagnosis(), testRun, "x")
	if len(d) > len(full) {
		t.Errorf("the diagnosis alone (%d bytes) is longer than the body (%d)", len(d), len(full))
	}
}

// trickyOriginals are reports that try the markers: empty, with trailing
// newlines, with CRLF, with marker-like text.
func trickyOriginals() []string {
	return []string{
		"",
		"\n",
		"one line",
		"trailing newlines\n\n\n",
		"CRLF\r\nline\r\n",
		"before\n" + endMark + "\nafter",
		"\n" + endMark,
		endMark + "\n",
		beginMark + "\ninside\n" + endMark,
		"<!-- agent-fox:triage v1 run=other -->\n## Diagnosis\nnested",
		"## Original report\n\n<details>",
		"`code` and ```fences``` and <b>html</b>",
		"é ü 日本語 \u2028 \x00",
	}
}

// TestTheOriginalSitsBetweenTheMarkers_TS_08_44 verifies TS-08-44:
// the bytes between `<begin>\n` and `\n<end>` equal the original, with nothing
// else added there.
// Verifies: 08-REQ-7.2
func TestTheOriginalSitsBetweenTheMarkers_TS_08_44(t *testing.T) {
	for _, o := range trickyOriginals() {
		body := Render(fullDiagnosis(), testRun, o)
		i := strings.Index(body, beginMark+"\n")
		j := strings.LastIndex(body, "\n"+endMark)
		if i < 0 || j < 0 {
			t.Fatalf("no markers in the body for %q", o)
		}
		if got := body[i+len(beginMark)+1 : j]; got != o {
			t.Errorf("between the markers: %q, want the original %q", got, o)
		}
	}
}

// TestABodyThatIsNotOursIsWhollyTheOriginal_TS_08_45 verifies TS-08-45:
// a body whose first line is not a triage marker is wholly the original.
// Verifies: 08-REQ-7.3
func TestABodyThatIsNotOursIsWhollyTheOriginal_TS_08_45(t *testing.T) {
	for name, body := range map[string]string{
		"a person's body":                        "The login button does nothing.\n\nSteps:\n1. click",
		"an empty body":                          "",
		"the markers, but not on the first line": "intro\n" + beginMark + "\nx\n" + endMark + "\n",
		"a triage marker on the second line":     "intro\n<!-- agent-fox:triage v1 run=x -->\n" + beginMark + "\nx\n" + endMark,
	} {
		t.Run(name, func(t *testing.T) {
			original, previous, warning := Extract(body)
			if original != body || previous != "" || warning != "" {
				t.Errorf("Extract = (%q, %q, %q), want (the body, \"\", \"\")", original, previous, warning)
			}
		})
	}
}

// TestAPairedBodyYieldsItsOriginalAndPreviousDiagnosis_TS_08_46 verifies TS-08-46:
// the original is between the first begin marker and the last end marker, the
// previous diagnosis is everything between the first line and the original-report
// heading.
// Verifies: 08-REQ-7.4
func TestAPairedBodyYieldsItsOriginalAndPreviousDiagnosis_TS_08_46(t *testing.T) {
	for _, s := range trickyOriginals() {
		body := Render(fullDiagnosis(), testRun, s)

		original, previous, warning := Extract(body)

		if original != s {
			t.Errorf("original = %q, want %q", original, s)
		}
		if !strings.HasPrefix(previous, "## Diagnosis") || strings.Contains(previous, "## Original report") || strings.Contains(previous, "<!-- agent-fox:triage") {
			t.Errorf("previous diagnosis = %q, want everything between the first line and ## Original report", previous)
		}
		if !strings.Contains(previous, "The handler dereferences a nil session.") || !strings.Contains(previous, "<sub>Diagnosed by agent-fox") {
			t.Errorf("previous diagnosis lacks the diagnosis:\n%s", previous)
		}
		if warning != "" {
			t.Errorf("warning = %q, want none", warning)
		}
	}
}

// TestAnUnpairableTriageBodyIsWhollyTheOriginalWithAWarning_TS_08_47 verifies TS-08-47:
// a body that starts with the triage marker and whose markers cannot be paired
// is wholly the original, with the warning original_report_unparsed.
// Verifies: 08-REQ-7.5
func TestAnUnpairableTriageBodyIsWhollyTheOriginalWithAWarning_TS_08_47(t *testing.T) {
	first := "<!-- agent-fox:triage v1 run=" + testRun + " -->\n"
	for name, body := range map[string]string{
		"no markers":                 first + "## Diagnosis\n\ntext\n",
		"only the begin marker":      first + "## Diagnosis\n\n" + beginMark + "\nlost\n",
		"only the end marker":        first + "## Diagnosis\n\nlost\n" + endMark + "\n",
		"the end before the begin":   first + endMark + "\nmid\n" + beginMark + "\ntail",
		"the marker alone":           strings.TrimSuffix(first, "\n"),
		"a begin with no newline":    first + beginMark + "\n" + endMark,
		"a begin and no end newline": first + beginMark + "\nx" + endMark,
	} {
		t.Run(name, func(t *testing.T) {
			original, previous, warning := Extract(body)
			if original != body || previous != "" || warning != "original_report_unparsed" {
				t.Errorf("Extract = (%q, %q, %q), want (the body, \"\", original_report_unparsed)", original, previous, warning)
			}
		})
	}
}

// randomText is a string built from pieces that try the markers and the
// layout of a body.
func randomText(rng *rand.Rand) string {
	pieces := []string{
		"", "a", " ", "\n", "\r\n", "\t", "`", "```", "<", ">", "<!--", "-->", beginMark, endMark, "\n" + endMark, endMark + "\n", beginMark + "\n",
		"<!-- agent-fox:triage v1 run=x -->", "## Original report", "## Diagnosis", "</details>", "<details>", "é", "日本", "\u2028", "\x00", "\xff",
	}
	var b strings.Builder
	for range rng.Intn(12) {
		b.WriteString(pieces[rng.Intn(len(pieces))])
	}
	return b.String()
}

// TestExtractInvertsRender_TS_08_48 verifies TS-08-48:
// for any string s, extracting from the body rendered around it returns s byte
// for byte, and rendering again from the extracted original gives the same
// body.
// Verifies: 08-REQ-7.6
func TestExtractInvertsRender_TS_08_48(t *testing.T) {
	rng := rand.New(rand.NewSource(48))
	diagnoses := []Diagnosis{fullDiagnosis(), notReproducible()}
	cases := append([]string{}, trickyOriginals()...)
	for range 2000 {
		cases = append(cases, randomText(rng))
	}
	for _, s := range cases {
		for _, d := range diagnoses {
			body := Render(d, testRun, s)

			original, _, warning := Extract(body)

			if original != s || warning != "" {
				t.Fatalf("Extract(Render(d, %q)) = (%q, warning %q), want the original and no warning", s, original, warning)
			}
			if again := Render(d, testRun, original); again != body {
				t.Fatalf("Render(d, Extract(body).original) differs from the body for %q", s)
			}
		}
	}
}

// TestAPreviousDiagnosisThatHoldsTheHeadingIsKeptWhole guards 08-REQ-7.4: the
// original-report heading that ends the previous diagnosis is the one before the
// begin marker, whatever the diagnosis itself says.
func TestAPreviousDiagnosisThatHoldsTheHeadingIsKeptWhole(t *testing.T) {
	d := fullDiagnosis()
	d.RootCause = "It quotes the heading:\n## Original report\nand goes on."
	body := Render(d, testRun, "the report")

	original, previous, warning := Extract(body)

	if original != "the report" || warning != "" {
		t.Fatalf("Extract = (%q, _, %q)", original, warning)
	}
	if !strings.Contains(previous, "and goes on.") || !strings.Contains(previous, "<sub>Diagnosed by agent-fox") {
		t.Errorf("the previous diagnosis stops at a heading the diagnosis quoted:\n%s", previous)
	}
}
