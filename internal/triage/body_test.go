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

// The golden files show every section of a full diagnosis, and only the
// diagnosis and the original report of one that found nothing.
func TestRender_IsDeterministicAndMatchesTheGoldenFiles(t *testing.T) {
	const original = "The login button does nothing.\nIt used to work.\n"
	full := Render(fullDiagnosis(), testRun, original)
	if again := Render(fullDiagnosis(), testRun, original); again != full {
		t.Error("two renders of the same diagnosis, run and original differ")
	}
	golden(t, "body_full.md", full)
	golden(t, "body_not_reproducible.md", Render(notReproducible(), testRun, "It crashes sometimes."))
}

func TestRenderDiagnosisOnly_LeavesOutTheOriginalReport(t *testing.T) {
	d := RenderDiagnosisOnly(fullDiagnosis(), testRun)

	for _, absent := range []string{"## Original report", BeginMarker, EndMarker, "<details>"} {
		if strings.Contains(d, absent) {
			t.Errorf("the diagnosis alone contains %q:\n%s", absent, d)
		}
	}
	part, _, _ := strings.Cut(Render(fullDiagnosis(), testRun, "x"), "\n\n## Original report")
	if want := strings.Replace(part, " The original report is kept below.", "", 1) + "\n"; d != want {
		t.Errorf("the diagnosis alone =\n%s\nwant the diagnosis of the body, its attribution without the pointer to the report:\n%s", d, want)
	}
}

// trickyOriginals are reports that try the markers: empty, with trailing
// newlines, with CRLF, with marker-like text.
var trickyOriginals = []string{
	"", "\n", "one line", "trailing newlines\n\n\n", "CRLF\r\nline\r\n",
	"before\n" + EndMarker + "\nafter", "\n" + EndMarker, EndMarker + "\n",
	BeginMarker + "\ninside\n" + EndMarker,
	"<!-- agent-fox:triage v1 run=other -->\n## Diagnosis\nnested",
	"## Original report\n\n<details>",
	"`code` and ```fences``` and <b>html</b>",
	"é ü 日本語 \u2028 \x00",
}

// randomText is a string built from pieces that try the markers and the layout
// of a body.
func randomText(rng *rand.Rand) string {
	pieces := []string{
		"", "a", " ", "\n", "\r\n", "\t", "`", "```", "<", ">", "<!--", "-->", BeginMarker, EndMarker, "\n" + EndMarker, EndMarker + "\n", BeginMarker + "\n",
		"<!-- agent-fox:triage v1 run=x -->", "## Original report", "## Diagnosis", "</details>", "<details>", "é", "日本", "\u2028", "\x00", "\xff",
	}
	var b strings.Builder
	for range rng.Intn(12) {
		b.WriteString(pieces[rng.Intn(len(pieces))])
	}
	return b.String()
}

// For any original, the bytes between `<begin>\n` and `\n<end>` are the
// original, Extract gives it back with the diagnosis before it, and rendering
// again gives the same body.
func TestExtract_InvertsRender(t *testing.T) {
	rng := rand.New(rand.NewSource(48))
	cases := append([]string{}, trickyOriginals...)
	for range 2000 {
		cases = append(cases, randomText(rng))
	}
	for _, s := range cases {
		for _, d := range []Diagnosis{fullDiagnosis(), notReproducible()} {
			for _, run := range []string{testRun, "x y"} {
				body := Render(d, run, s)
				i, j := strings.Index(body, BeginMarker+"\n"), strings.LastIndex(body, "\n"+EndMarker)
				if i < 0 || j < 0 || body[i+len(BeginMarker)+1:j] != s {
					t.Fatalf("the markers of the body do not enclose the original %q", s)
				}

				original, previous, warning := Extract(body)

				if original != s || warning != "" {
					t.Fatalf("Extract(Render(%q)) = (%q, warning %q), want the original and no warning", s, original, warning)
				}
				if again := Render(d, run, original); again != body {
					t.Fatalf("rendering the extracted original %q again gives another body", s)
				}
				if !strings.HasPrefix(previous, "## Diagnosis") || !strings.Contains(previous, d.RootCause) ||
					!strings.Contains(previous, "<sub>Diagnosed by agent-fox `af triage` (run "+run+")") {
					t.Fatalf("the previous diagnosis of %q is %q, want the diagnosis up to its attribution", s, previous)
				}
			}
		}
	}
}

func TestExtract_ABodyThatIsNotOursIsWhollyTheOriginal(t *testing.T) {
	for name, body := range map[string]string{
		"a person's body":                        "The login button does nothing.\n\nSteps:\n1. click",
		"an empty body":                          "",
		"the markers, but not on the first line": "intro\n" + BeginMarker + "\nx\n" + EndMarker + "\n",
		"a triage marker on the second line":     "intro\n<!-- agent-fox:triage v1 run=x -->\n" + BeginMarker + "\nx\n" + EndMarker,
	} {
		if original, previous, warning := Extract(body); original != body || previous != "" || warning != "" {
			t.Errorf("%s: Extract = (%q, %q, %q), want the body, no diagnosis, no warning", name, original, previous, warning)
		}
	}
}

// A body that starts like ours and cannot be read back is taken whole as the
// report, with a warning: losing a person's words is worse than nesting an old
// diagnosis in the report.
func TestExtract_AnUnreadableBodyOfOursIsWhollyTheOriginalWithAWarning(t *testing.T) {
	first := "<!-- agent-fox:triage v1 run=" + testRun + " -->\n"
	body := Render(fullDiagnosis(), testRun, "the report")
	for name, edited := range map[string]string{
		"no markers":                 first + "## Diagnosis\n\ntext\n",
		"only the begin marker":      first + "## Diagnosis\n\n" + BeginMarker + "\nlost\n",
		"only the end marker":        first + "## Diagnosis\n\nlost\n" + EndMarker + "\n",
		"the end before the begin":   first + EndMarker + "\nmid\n" + BeginMarker + "\ntail",
		"the marker alone":           strings.TrimSuffix(first, "\n"),
		"a begin with no newline":    first + BeginMarker + "\n" + EndMarker,
		"a begin and no end newline": first + BeginMarker + "\nx" + EndMarker,
		"no original-report heading": first + BeginMarker + "\nx\n" + EndMarker + "\n</details>\n",
		"a marker line cut short":    "<!-- agent-fox:triage v1 run=" + testRun + "\nsome text",
		"a marker line with more":    "<!-- agent-fox:triage v1 run=" + testRun + " --> trailing\nsome text",
		"a line after the details":   body + "\nEDIT: it also happens on Windows.\n",
		"a line inside the details":  strings.Replace(body, EndMarker+"\n", EndMarker+"\nEDIT: more\n", 1),
	} {
		if original, previous, warning := Extract(edited); original != edited || previous != "" || warning != WarnOriginalUnparsed {
			t.Errorf("%s: Extract = (%q, %q, %q), want the body and %s", name, original, previous, warning, WarnOriginalUnparsed)
		}
	}
}

// The editor of a forge saves a body with CRLF endings, or with other white
// space around the close of the details block, and the body is still ours; its
// original is read with LF endings.
func TestExtract_ABodyAnEditorSavedAgainIsStillOurs(t *testing.T) {
	const person = "The login button does nothing.\nIt used to work."
	body := Render(fullDiagnosis(), testRun, person)
	for name, edited := range map[string]string{
		"CRLF endings":      strings.ReplaceAll(body, "\n", "\r\n"),
		"no final newline":  strings.TrimSuffix(body, "\n"),
		"extra white space": body + "\n\n",
		"a space before":    strings.Replace(body, "\n\n</details>", "\n \n</details>", 1),
	} {
		original, previous, warning := Extract(edited)
		if original != person || warning != "" || !strings.HasPrefix(previous, "## Diagnosis") || strings.Contains(previous, "\r") {
			t.Errorf("%s: Extract = (%q, %q, %q), want the report with LF endings and no warning", name, original, previous, warning)
		}
	}
}

func TestExtract_APreviousDiagnosisThatQuotesTheHeadingIsKeptWhole(t *testing.T) {
	d := fullDiagnosis()
	d.RootCause = "It quotes the heading:\n## Original report\nand goes on."

	original, previous, warning := Extract(Render(d, testRun, "the report"))

	if original != "the report" || warning != "" || !strings.Contains(previous, "and goes on.") || !strings.Contains(previous, "<sub>Diagnosed by agent-fox") {
		t.Errorf("Extract = (%q, %q, %q), want the report and the whole diagnosis", original, previous, warning)
	}
}
