package afspec

import (
	"strings"
	"testing"
)

// TestValidFixturesPassEveryRule is the positive case: the checked-in
// fixtures satisfy schema validation and every rule C1 to C11.
func TestValidFixturesPassEveryRule(t *testing.T) {
	for _, fixture := range []string{
		fixtureValidSpec,
		fixtureV2Example,
		"../testdata/draft_spec",
		"../testdata/alpha_prefix_spec",
		"../testdata/valid_spec_with_arch",
	} {
		t.Run(fixture, func(t *testing.T) {
			spec := loadFixture(t, fixture)
			result := spec.Validate()
			if !result.Valid {
				t.Fatalf("Validate reported %d errors on a valid fixture: %v",
					len(result.Errors), result.Errors)
			}
		})
	}
}

func TestValidateSchemaAcceptsTheCanonicalFixture(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	if result := spec.ValidateSchema(); !result.Valid {
		t.Fatalf("ValidateSchema failed on the canonical fixture: %v", result.Errors)
	}
}

// ---------------------------------------------------------------------------
// One negative case per cross-file rule (§10.2)
// ---------------------------------------------------------------------------

// mutateFixture loads the canonical fixture and applies mutate to it.
func mutateFixture(t *testing.T, mutate func(*Spec)) *Spec {
	t.Helper()
	spec := loadFixture(t, fixtureValidSpec)
	mutate(spec)
	return spec
}

// wantCheck asserts that validation fails with at least one error carrying the
// given rule name.
func wantCheck(t *testing.T, spec *Spec, check string) ValidationResult {
	t.Helper()
	result := spec.ValidateCrossFile()
	if result.Valid {
		t.Fatalf("ValidateCrossFile reported the spec as valid; want a %s violation", check)
	}
	if !hasCheck(result, check) {
		t.Fatalf("no %s error; got checks %v (messages: %v)", check, errorChecks(result), result.Errors)
	}
	return result
}

func TestC1SpecIdentityMustAgreeAcrossArtifacts(t *testing.T) {
	t.Run("requirements disagrees on spec_id", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) { s.Requirements.SpecId = "99" })
		wantCheck(t, spec, "C1")
	})
	t.Run("tasks disagrees on spec_name", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) { s.Tasks.SpecName = "other_name" })
		wantCheck(t, spec, "C1")
	})
	t.Run("folder name disagrees with spec_id", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) { s.Dir = "/tmp/07_test_feature" })
		wantCheck(t, spec, "C1")
	})
}

func TestC2IdFormatsPrefixesAndUniqueness(t *testing.T) {
	t.Run("criterion ID in the wrong format", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Requirements.Requirements[0].Criteria[0].Id = "01-REQ-1.E1"
		})
		wantCheck(t, spec, "C2")
	})
	t.Run("test ID carries another spec's prefix", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) { s.TestSpec.Tests[0].Id = "TS-77-1" })
		wantCheck(t, spec, "C2")
	})
	t.Run("duplicate task ID", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) { s.Tasks.Tasks[1].Id = s.Tasks.Tasks[0].Id })
		wantCheck(t, spec, "C2")
	})
	t.Run("criterion listed under the wrong requirement", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Requirements.Requirements[1].Criteria[0].Id = "01-REQ-1.9"
		})
		wantCheck(t, spec, "C2")
	})
}

func TestC3VerifiesMustResolve(t *testing.T) {
	spec := mutateFixture(t, func(s *Spec) {
		s.TestSpec.Tests[0].Verifies = []string{"01-REQ-9.9"}
	})
	result := wantCheck(t, spec, "C3")
	if !strings.Contains(result.Errors[0].Message, "01-REQ-9.9") {
		t.Errorf("the error does not name the dangling reference: %q", result.Errors[0].Message)
	}
}

func TestC4EveryCriterionMustBeVerified(t *testing.T) {
	spec := mutateFixture(t, func(s *Spec) {
		// TS-01-4 is the only test verifying 01-REQ-2.1.
		s.TestSpec.Tests[3].Verifies = []string{"01-REQ-1.1"}
		// Drop it from the tasks too, or C4 would be masked by nothing else.
	})
	result := wantCheck(t, spec, "C4")
	if !hasEntityID(result, "01-REQ-2.1") {
		t.Errorf("C4 did not name the uncovered criterion; got %v", result.Errors)
	}
}

func TestC5PathsNeedSmokeTests(t *testing.T) {
	t.Run("path verified only by a unit test", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			// Demote the smoke test; the path is then verified by nothing.
			s.TestSpec.Tests[4].Kind = TestKindUnit
			s.TestSpec.Tests[4].RealComponents = nil
			s.Tasks.Tasks[2].Kind = TaskKindIntegration
		})
		result := wantCheck(t, spec, "C5")
		if !hasEntityID(result, "01-PATH-1") {
			t.Errorf("C5 did not name the unverified path; got %v", result.Errors)
		}
	})
	t.Run("smoke test verifying no path", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.TestSpec.Tests[4].Verifies = []string{"01-REQ-1.1"}
		})
		wantCheck(t, spec, "C5")
	})
}

func TestC6TaskReferencesMustResolve(t *testing.T) {
	t.Run("unknown criterion", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Tasks.Tasks[0].Criteria = []string{"01-REQ-8"}
		})
		wantCheck(t, spec, "C6")
	})
	t.Run("unknown test", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Tasks.Tasks[0].Tests = []string{"TS-01-99"}
		})
		wantCheck(t, spec, "C6")
	})
	t.Run("depends_on names a missing task", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Tasks.Tasks[1].DependsOn = []int{42}
		})
		wantCheck(t, spec, "C6")
	})
	t.Run("depends_on must reference lower IDs", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Tasks.Tasks[0].DependsOn = []int{3}
		})
		result := wantCheck(t, spec, "C6")
		if !strings.Contains(strings.Join(messages(result), " "), "lower task IDs") {
			t.Errorf("the error does not explain the DAG rule: %v", messages(result))
		}
	})
}

// TestC7EveryTestIsOwnedByATask covers the defect that motivated format v2:
// under v1 every edge-case and property test in every archived spec was owned
// by no task, and validation accepted it.
func TestC7EveryTestIsOwnedByATask(t *testing.T) {
	spec := mutateFixture(t, func(s *Spec) {
		// Orphan TS-01-3, the property test.
		s.Tasks.Tasks[0].Tests = []string{"TS-01-1", "TS-01-2"}
	})
	result := wantCheck(t, spec, "C7")
	if !hasEntityID(result, "TS-01-3") {
		t.Errorf("C7 did not name the orphaned test; got %v", result.Errors)
	}
}

func TestC8EveryCriterionIsOwnedByAnImplementTask(t *testing.T) {
	t.Run("criterion claimed by no task", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Tasks.Tasks[1].Criteria = []string{"01-REQ-1.1"}
		})
		result := wantCheck(t, spec, "C8")
		if !hasEntityID(result, "01-REQ-2.1") {
			t.Errorf("C8 did not name the unowned criterion; got %v", result.Errors)
		}
	})
	t.Run("only the integration task claims it", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Tasks.Tasks[1].Criteria = []string{"01-REQ-1.1"}
			s.Tasks.Tasks[2].Criteria = []string{"01-REQ-2.1"}
		})
		wantCheck(t, spec, "C8")
	})
	t.Run("a requirement ID covers all of its criteria", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Tasks.Tasks[0].Criteria = []string{"01-REQ-1"}
			s.Tasks.Tasks[1].Criteria = []string{"01-REQ-2"}
		})
		if result := spec.ValidateCrossFile(); !result.Valid {
			t.Fatalf("claiming a requirement wholesale should cover its criteria; got %v", result.Errors)
		}
	})
}

func TestC9IntegrationTask(t *testing.T) {
	t.Run("no integration task", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Tasks.Tasks[2].Kind = TaskKindImplement
			s.Tasks.Tasks[2].Criteria = []string{"01-REQ-2.1"}
		})
		wantCheck(t, spec, "C9")
	})
	t.Run("integration task is not last", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Tasks.Tasks[0].Kind, s.Tasks.Tasks[2].Kind = TaskKindIntegration, TaskKindImplement
			s.Tasks.Tasks[2].Criteria = []string{"01-REQ-2.1"}
			s.Tasks.Tasks[0].Tests = append(s.Tasks.Tasks[0].Tests, "TS-01-5")
		})
		wantCheck(t, spec, "C9")
	})
	t.Run("integration task does not own every smoke test", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.Tasks.Tasks[2].Tests = []string{"TS-01-1"}
			s.Tasks.Tasks[0].Tests = append(s.Tasks.Tasks[0].Tests, "TS-01-5")
		})
		result := wantCheck(t, spec, "C9")
		if !hasEntityID(result, "TS-01-5") {
			t.Errorf("C9 did not name the unowned smoke test; got %v", result.Errors)
		}
	})
	t.Run("two integration tasks", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) { s.Tasks.Tasks[1].Kind = TaskKindIntegration })
		wantCheck(t, spec, "C9")
	})
}

func TestC10UnwantedCriteriaNeedAContract(t *testing.T) {
	spec := mutateFixture(t, func(s *Spec) {
		// 01-REQ-1.2 is the unwanted criterion.
		s.Requirements.Requirements[0].Criteria[1].Contract = nil
	})
	result := wantCheck(t, spec, "C10")
	if !hasEntityID(result, "01-REQ-1.2") {
		t.Errorf("C10 did not name the criterion; got %v", result.Errors)
	}
}

func TestC11RealComponentsBelongToSmokeTestsOnly(t *testing.T) {
	t.Run("smoke test without real components", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) { s.TestSpec.Tests[4].RealComponents = nil })
		wantCheck(t, spec, "C11")
	})
	t.Run("unit test with real components", func(t *testing.T) {
		spec := mutateFixture(t, func(s *Spec) {
			s.TestSpec.Tests[0].RealComponents = []string{"a database"}
		})
		wantCheck(t, spec, "C11")
	})
}

// ---------------------------------------------------------------------------
// Warnings never block
// ---------------------------------------------------------------------------

func TestWarningsDoNotInvalidateASpec(t *testing.T) {
	spec := mutateFixture(t, func(s *Spec) {
		s.Requirements.Requirements[0].Criteria[0].Action =
			"handle the request appropriately and return a reasonable result"
	})
	result := spec.Validate()
	if !result.Valid {
		t.Fatalf("vague language must warn, not fail; got %v", result.Errors)
	}
	if !hasWarningContaining(result, "vague term") {
		t.Errorf("no vague-language warning; got %v", result.Warnings)
	}
}

func TestScopeLimitsWarn(t *testing.T) {
	spec := mutateFixture(t, func(s *Spec) {
		s.Tasks.Tasks[0].Steps = make([]string, maxStepsPerTask+1)
		for i := range s.Tasks.Tasks[0].Steps {
			s.Tasks.Tasks[0].Steps[i] = "do a thing"
		}
	})
	result := spec.ValidateCrossFile()
	if !result.Valid {
		t.Fatalf("a task over the step limit must warn, not fail; got %v", result.Errors)
	}
	if !hasWarningContaining(result, "steps (limit 12)") {
		t.Errorf("no step-count warning; got %v", result.Warnings)
	}
}

// TestGlossaryIsAWarningNotAnError guards §10.3: the v1 rule made every
// backticked identifier a validation error and produced most of the repair
// churn. In v2 it is at most a warning, and only from three uses.
func TestGlossaryIsAWarningNotAnError(t *testing.T) {
	spec := mutateFixture(t, func(s *Spec) {
		for i := range s.Requirements.Requirements[0].Criteria {
			s.Requirements.Requirements[0].Criteria[i].Action += " using the `WidgetRegistry`"
		}
	})
	result := spec.ValidateCrossFile()
	if !result.Valid {
		t.Fatalf("an undefined glossary term must never be an error; got %v", result.Errors)
	}
	if !hasWarningContaining(result, "WidgetRegistry") {
		t.Errorf("no glossary hint for a term used in three criteria; got %v", result.Warnings)
	}
}

func TestGlossaryHintIsSilentBelowThreeUses(t *testing.T) {
	spec := mutateFixture(t, func(s *Spec) {
		s.Requirements.Requirements[0].Criteria[0].Action += " using the `WidgetRegistry`"
	})
	if hasWarningContaining(spec.ValidateCrossFile(), "WidgetRegistry") {
		t.Error("a term used in one criterion produced a glossary hint")
	}
}

func TestErrorOutcomeWithoutContractWarns(t *testing.T) {
	spec := mutateFixture(t, func(s *Spec) {
		c := &s.Requirements.Requirements[0].Criteria[2] // the ubiquitous one
		c.Action = "for any malformed input, reject the request as invalid"
		c.Contract = nil
	})
	result := spec.ValidateCrossFile()
	if !result.Valid {
		t.Fatalf("a missing contract on a non-unwanted criterion must warn, not fail; got %v", result.Errors)
	}
	if !hasWarningContaining(result, "describes an error outcome but has no contract") {
		t.Errorf("no contract hint; got %v", result.Warnings)
	}
}

// ---------------------------------------------------------------------------
// Scaffolds and incomplete specs
// ---------------------------------------------------------------------------

func TestScaffoldIsIncompleteNotSchemaInvalid(t *testing.T) {
	spec := CreateSpec("07", "new_feature")
	spec.Title = "New Feature"
	spec.PRDBody = "# New Feature\n\n## Intent\n\nSomething.\n"

	if !spec.IsScaffold() {
		t.Fatal("a freshly created spec is not reported as a scaffold")
	}

	result := spec.Validate()
	if result.Valid {
		t.Fatal("an empty scaffold must not validate")
	}
	if len(result.Errors) != 1 || result.Errors[0].Check != "completeness" {
		t.Fatalf("want a single completeness error, got %v", result.Errors)
	}
	if !strings.Contains(result.Errors[0].Message, "spec generate") {
		t.Errorf("the completeness message does not say what to do next: %q", result.Errors[0].Message)
	}
}

func TestScaffoldSurvivesASaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	spec := CreateSpec("07", "new_feature")
	spec.Title = "New Feature"
	spec.Status = "draft"
	spec.CreatedAt = "2026-01-01T00:00:00Z"
	spec.UpdatedAt = "2026-01-01T00:00:00Z"
	spec.PRDBody = "# New Feature\n\n## Intent\n\nSomething.\n"

	if err := spec.Save(dir); err != nil {
		t.Fatalf("Save of a scaffold = %v", err)
	}
	reloaded, err := LoadSpec(dir)
	if err != nil {
		t.Fatalf("LoadSpec of a scaffold = %v; a scaffold must load so that `spec generate` can fill it in", err)
	}
	if !reloaded.IsScaffold() {
		t.Error("the reloaded scaffold is not reported as one")
	}
}

func TestMissingArtifactIsReportedAsIncomplete(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	spec.Tasks = nil
	result := spec.ValidateCrossFile()
	if result.Valid {
		t.Fatal("a spec with no tasks artifact validated")
	}
	if result.Errors[0].Check != "completeness" {
		t.Errorf("check = %q; want completeness", result.Errors[0].Check)
	}
}

// ---------------------------------------------------------------------------
// ValidateStructured
// ---------------------------------------------------------------------------

func TestValidateStructuredShape(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	out := spec.ValidateStructured()
	if valid, _ := out["valid"].(bool); !valid {
		t.Fatalf("valid = false on the canonical fixture: %v", out["errors"])
	}
	if _, ok := out["errors"].([]map[string]any); !ok {
		t.Errorf("errors key has type %T; want []map[string]any", out["errors"])
	}
	if _, present := out["warnings"]; present {
		t.Errorf("the warnings key is present with no warnings: %v", out["warnings"])
	}

	broken := mutateFixture(t, func(s *Spec) { s.Tasks.Tasks[0].Tests = []string{"TS-01-1"} })
	out = broken.ValidateStructured()
	if valid, _ := out["valid"].(bool); valid {
		t.Fatal("valid = true on a spec with orphaned tests")
	}
	entries := out["errors"].([]map[string]any)
	if len(entries) == 0 {
		t.Fatal("no error entries")
	}
	if entries[0]["category"] != "integrity" || entries[0]["check"] == "" {
		t.Errorf("integrity entry has the wrong shape: %v", entries[0])
	}
}

func hasEntityID(result ValidationResult, id string) bool {
	for _, e := range result.Errors {
		if e.EntityID == id {
			return true
		}
	}
	return false
}

func messages(result ValidationResult) []string {
	out := make([]string, 0, len(result.Errors))
	for _, e := range result.Errors {
		out = append(out, e.Message)
	}
	return out
}
