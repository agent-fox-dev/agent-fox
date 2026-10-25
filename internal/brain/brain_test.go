package brain

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestBrains_SubmitTheSameBytesForTheSamePayload(t *testing.T) {
	payloads := []string{
		`{"summary":"fixed","cite":"README.md"}`,
		"{\n  \"cite\": \"README.md\",\n  \"files\": [\"src/main.go\"],\n  \"n\": 1.5\n}",
		`{ "ünï": "☃ <b>&</b>", "line": "a\nb ", "cite": "README.md", "none": null }`,
	}
	got := map[string][]string{}
	for _, name := range []string{ScriptedName, AgentkitName} {
		w := newWorld(t)
		for i, payload := range payloads {
			var brain Brain
			if name == ScriptedName {
				brain = must(Default.New(ScriptedName, Options{Script: writeScript(t, `{"steps":[{"submit":`+payload+`,"expect":"accepted"}]}`)}))
			} else {
				brain, _ = fauxBrain(t, call("c1", "submit_test", payload))
			}
			b := w.brief(i + 1)
			b.Kind = citeKind()
			req := w.request(b, brain)
			req.Name = name

			res, err := Execute(context.Background(), req)

			host, _ := w.hostSubmission()
			if err != nil || !res.Outcome.Submitted || !json.Valid(res.Payload) {
				t.Fatalf("%s, payload %d: %+v, %v", name, i, res, err)
			}
			got[name] = append(got[name], string(testutil.Decode[struct{ Payload json.RawMessage }](t, []byte(host)).Payload))
		}
	}
	for i := range payloads {
		if got[ScriptedName][i] != got[AgentkitName][i] {
			t.Errorf("payload %d: scripted submitted %s and agentkit %s", i, got[ScriptedName][i], got[AgentkitName][i])
		}
	}
}
