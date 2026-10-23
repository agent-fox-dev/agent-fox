package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
)

// check validates v against the JSON Schema schema, for the part of JSON Schema that the schemas of
// af use: type, enum, const, required, properties, additionalProperties, items and oneOf. It returns
// the first thing that does not fit, with the path to it.
func check(schema map[string]any, v any, path string) error {
	if alternatives, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, alt := range alternatives {
			if check(alt.(map[string]any), v, path) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: matches %d of the %d alternatives of oneOf, want exactly 1", path, matches, len(alternatives))
		}
		return nil
	}
	if want, ok := schema["const"]; ok && !reflect.DeepEqual(want, v) {
		return fmt.Errorf("%s: is %v, want the constant %v", path, v, want)
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			found = found || reflect.DeepEqual(e, v)
		}
		if !found {
			return fmt.Errorf("%s: is %v, want one of %v", path, v, enum)
		}
	}
	if typ, ok := schema["type"]; ok {
		var types []string
		switch t := typ.(type) {
		case string:
			types = []string{t}
		case []any:
			for _, x := range t {
				types = append(types, x.(string))
			}
		}
		matched := false
		for _, t := range types {
			matched = matched || isType(t, v)
		}
		if !matched {
			return fmt.Errorf("%s: is %T, want %v", path, v, types)
		}
	}
	switch v := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, r := range required {
				if _, present := v[r.(string)]; !present {
					return fmt.Errorf("%s: lacks the required %s", path, r)
				}
			}
		}
		for k, x := range v {
			sub, known := props[k].(map[string]any)
			if !known {
				if schema["additionalProperties"] == false {
					return fmt.Errorf("%s: has the field %s, which the schema does not allow", path, k)
				}
				continue
			}
			if err := check(sub, x, path+"."+k); err != nil {
				return err
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, x := range v {
				if err := check(items, x, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func isType(t string, v any) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "number", "integer":
		_, ok := v.(float64)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	}
	return false
}

// schemaOf is the JSON Schema of the embedded file.
func schemaOf(t *testing.T, name string) map[string]any {
	t.Helper()
	var s map[string]any
	if err := json.Unmarshal(resultSchema(name), &s); err != nil {
		t.Fatalf("schemas/%s.json: %v", name, err)
	}
	return s
}

// jsonFields are the JSON names of the fields of a struct type.
func jsonFields(typ reflect.Type) []string {
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// TestEveryNewCommandRegistersASchemaThatMatchesItsTypeAndAllowsOtherFields_TS_16_15 verifies
// TS-16-15 for the new commands: each is in af --schema, the top-level properties of each schema are
// the JSON fields of its Go type, and a result with fields the schema does not know validates.
// The golden file is checked byte for byte by TestSchemaGolden_TS_05_70.
// Verifies: 16-REQ-2.7
func TestEveryNewCommandRegistersASchemaThatMatchesItsTypeAndAllowsOtherFields_TS_16_15(t *testing.T) {
	code, stdout, _ := rawRun(t, "", "--schema")
	if code != 0 {
		t.Fatalf("--schema: exit %d", code)
	}
	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"login", "workspace list", "workspace subscribe", "workspace get", "workspace unsubscribe",
		"jobs list", "jobs get", "jobs watch", "jobs cancel", "jobs answer", "jobs retry"} {
		if _, ok := doc.Defs[strings.ReplaceAll(path, " ", "_")]; !ok {
			t.Errorf("af --schema has no $defs.%s", strings.ReplaceAll(path, " ", "_"))
		}
	}

	for name, typ := range map[string]reflect.Type{
		"login": reflect.TypeOf(loginResult{}), "slug": reflect.TypeOf(slugResult{}), "job_result": reflect.TypeOf(jobResult{}),
		"workspace_list": reflect.TypeOf(workspaceListDoc{}), "job_page": reflect.TypeOf(jobPageDoc{}), "subscription": reflect.TypeOf(subscriptionDoc{}),
	} {
		props, _ := schemaOf(t, name)["properties"].(map[string]any)
		var got []string
		for k := range props {
			got = append(got, k)
		}
		slices.Sort(got)
		if want := jsonFields(typ); !slices.Equal(got, want) {
			t.Errorf("schemas/%s.json has the properties %v, but %s has the JSON fields %v", name, got, typ.Name(), want)
		}
	}

	view := map[string]any{"slug": "w", "label_prefix": "af:", "token_user": nil, "subscribed_by": "u", "token_state": "ok", "created_at": "t", "updated_at": "t", "future": map[string]any{"a": []any{1}}}
	job := map[string]any{"id": "j", "state": "done", "future": true, "attempts": []any{map[string]any{"n": 1, "extra": 2}}}
	for name, samples := range map[string][]any{
		"login":          {map[string]any{"credential_file": "/h/.af/config.toml", "server": "http://127.0.0.1:1", "future": 1}},
		"slug":           {map[string]any{"slug": "w", "future": 1}},
		"job_result":     {map[string]any{"job": job, "envelope": nil}, map[string]any{"job": job, "envelope": map[string]any{"status": "done", "future": 1}, "future": 1}},
		"workspace_list": {map[string]any{"workspaces": []any{view}, "future": 1}, map[string]any{"workspaces": []any{}}},
		"job_page":       {map[string]any{"jobs": []any{job}, "next_cursor": "c", "future": 1}, map[string]any{"jobs": []any{}, "next_cursor": nil}},
		"subscription":   {view},
	} {
		for i, sample := range samples {
			if err := check(schemaOf(t, name), sample, "$"); err != nil {
				t.Errorf("schemas/%s.json, sample %d: %v", name, i, err)
			}
		}
	}
	// The check is not vacuous: a result that lacks what the schema requires does not validate.
	for name, bad := range map[string]any{
		"login": map[string]any{"server": "http://127.0.0.1:1"}, "slug": map[string]any{}, "job_result": map[string]any{"job": job},
		"workspace_list": map[string]any{"workspaces": "x"}, "job_page": map[string]any{"jobs": []any{}}, "subscription": map[string]any{"label_prefix": "x"},
	} {
		if err := check(schemaOf(t, name), bad, "$"); err == nil {
			t.Errorf("schemas/%s.json accepts %v", name, bad)
		}
	}
}

// TestTheSchemaOfTriageAndFixAdmitsTheLocalResultAndTheJobResult_TS_16_15 verifies TS-16-15 for
// af triage --schema and af fix --schema: the result is the local result or the job result.
// Verifies: 16-REQ-2.7
func TestTheSchemaOfTriageAndFixAdmitsTheLocalResultAndTheJobResult_TS_16_15(t *testing.T) {
	e := newLocalEnv(t, nil)
	code, env := e.run("", e.loopArgs("confirmed", "--local")...)
	if code != 0 {
		t.Fatalf("triage: exit %d, %v", code, env["error"])
	}
	local := env["result"]
	jobSample := map[string]any{"job": map[string]any{"id": "j", "state": "queued", "future": 1}, "envelope": nil}

	for _, cmd := range []string{"triage", "fix"} {
		code, stdout, _ := rawRun(t, "", cmd, "--schema")
		if code != 0 {
			t.Fatalf("%s --schema: exit %d", cmd, code)
		}
		var doc struct {
			Defs map[string]map[string]any `json:"$defs"`
		}
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatal(err)
		}
		schema := doc.Defs[cmd]
		alts, _ := schema["oneOf"].([]any)
		if len(alts) != 2 {
			t.Fatalf("%s --schema: $defs.%s = %v, want a oneOf of the local result and the job result", cmd, cmd, schema)
		}
		// The first alternative is the schema of the loop exactly as it was, so that every local result
		// that was valid before still is (for fix too, whose result needs a whole fix to be made here).
		var before struct {
			Defs map[string]any `json:"$defs"`
		}
		var out bytes.Buffer
		afcmd.New(&afcmd.Config{}).Main(context.Background(), []string{"af", cmd, "--schema"}, strings.NewReader(""), &out, &bytes.Buffer{})
		if err := json.Unmarshal(out.Bytes(), &before); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(alts[0], before.Defs[cmd]) {
			t.Errorf("%s --schema: the first alternative differs from the schema of the loop that afcmd registers", cmd)
		}
		if !reflect.DeepEqual(alts[1], any(schemaOf(t, "job_result"))) {
			t.Errorf("%s --schema: the second alternative is not the job result", cmd)
		}
		if cmd == "triage" {
			if err := check(schema, local, "$"); err != nil {
				t.Errorf("triage --schema does not admit a local result: %v", err)
			}
		}
		if err := check(schema, jobSample, "$"); err != nil {
			t.Errorf("%s --schema does not admit a job result: %v", cmd, err)
		}
		if err := check(schema, map[string]any{"unrelated": 1}, "$"); err == nil {
			t.Errorf("%s --schema admits any object", cmd)
		}
	}
}
