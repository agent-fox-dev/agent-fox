package afspec

import (
	"errors"
	"testing"
)

// TestErrorHierarchy_AllTypesWrapSpecError verifies that all afspec error types
// implement the error interface and wrap SpecError such that
// errors.As(err, &SpecError{}) returns true.
// Test Spec: TS-01-50, Requirement: 01-REQ-26.1
func TestErrorHierarchy_AllTypesWrapSpecError(t *testing.T) {
	baseErr := &SpecError{Msg: "base error"}

	tests := []struct {
		name string
		err  error
	}{
		{
			name: "LoadError",
			err:  &LoadError{Msg: "load failed", File: "test.json", Err: baseErr},
		},
		{
			name: "SaveError",
			err:  &SaveError{Msg: "save failed", Err: baseErr},
		},
		{
			name: "LifecycleError",
			err:  &LifecycleError{Msg: "invalid transition", Err: baseErr},
		},
		{
			name: "IntentError",
			err:  &IntentError{Msg: "missing intent", Err: baseErr},
		},
		{
			name: "BootstrapError",
			err:  &BootstrapError{Msg: "bootstrap failed", Err: baseErr},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// All should implement error interface
			_ = tt.err.Error()

			// All should unwrap to SpecError
			var specErr *SpecError
			if !errors.As(tt.err, &specErr) {
				t.Errorf("errors.As(%T, &SpecError{}) = false, want true", tt.err)
			}
		})
	}
}

// TestErrorHierarchy_SpecificToBase verifies errors.As traversal from
// specific to base type: errors.As(loadErr, &LoadError{}) returns true,
// and errors.As(loadErr, &SpecError{}) also returns true for the same value.
// Test Spec: TS-01-51, Requirement: 01-REQ-26.2
func TestErrorHierarchy_SpecificToBase(t *testing.T) {
	baseErr := &SpecError{Msg: "file not found"}
	loadErr := &LoadError{
		Msg:  "missing required file",
		File: "requirements.json",
		Err:  baseErr,
	}

	// Should match as LoadError
	var le *LoadError
	if !errors.As(loadErr, &le) {
		t.Error("errors.As(loadErr, &LoadError{}) = false, want true")
	}

	// Should also match as SpecError (via Unwrap)
	var se *SpecError
	if !errors.As(loadErr, &se) {
		t.Error("errors.As(loadErr, &SpecError{}) = false, want true")
	}
}

// TestErrorHierarchy_LoadErrorFromLoadSpec verifies that LoadSpec returns
// errors that satisfy both LoadError and SpecError type assertions.
// Test Spec: TS-01-51, Requirement: 01-REQ-26.2
func TestErrorHierarchy_LoadErrorFromLoadSpec(t *testing.T) {
	defer requireImplemented(t)

	_, err := LoadSpec("/nonexistent/dir")
	if err == nil {
		t.Fatal("expected error from LoadSpec, got nil")
	}

	var loadErr *LoadError
	if !errors.As(err, &loadErr) {
		t.Errorf("errors.As(err, &LoadError{}) = false, want true; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T", err)
	}
}

// TestErrorHierarchy_LifecycleErrorFromSave verifies that Save on a sealed
// spec returns errors that satisfy both LifecycleError and SpecError.
// Test Spec: TS-01-50, Requirement: 01-REQ-26.1
func TestErrorHierarchy_LifecycleErrorFromSave(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		Status:        "sealed",
		SpecID:        "01",
		SpecName:      "test",
		SchemaVersion: 1,
	}

	err := spec.Save(t.TempDir())
	if err == nil {
		t.Fatal("expected error from Save on sealed spec, got nil")
	}

	var lifecycleErr *LifecycleError
	if !errors.As(err, &lifecycleErr) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T", err)
	}
}

// TestErrorHierarchy_NonAfspecError verifies that a non-afspec error does
// not satisfy errors.As(err, &SpecError{}).
// Requirement: 01-REQ-26.1
func TestErrorHierarchy_NonAfspecError(t *testing.T) {
	plainErr := errors.New("not an afspec error")

	var specErr *SpecError
	if errors.As(plainErr, &specErr) {
		t.Error("errors.As(plainErr, &SpecError{}) = true, want false for non-afspec error")
	}

	var loadErr *LoadError
	if errors.As(plainErr, &loadErr) {
		t.Error("errors.As(plainErr, &LoadError{}) = true, want false for non-afspec error")
	}
}

// TestErrorHierarchy_ErrorMessages verifies that all error types produce
// meaningful, non-empty error messages.
// Requirement: 01-REQ-26.1
func TestErrorHierarchy_ErrorMessages(t *testing.T) {
	baseErr := &SpecError{Msg: "base"}

	tests := []struct {
		name string
		err  error
	}{
		{"SpecError", &SpecError{Msg: "spec error"}},
		{"LoadError", &LoadError{Msg: "missing file", File: "req.json", Err: baseErr}},
		{"LoadError_NoFile", &LoadError{Msg: "parse error", Err: baseErr}},
		{"SaveError", &SaveError{Msg: "write failed", Err: baseErr}},
		{"LifecycleError", &LifecycleError{Msg: "invalid transition", Err: baseErr}},
		{"IntentError", &IntentError{Msg: "no intent section", Err: baseErr}},
		{"BootstrapError", &BootstrapError{Msg: "missing artifacts", Err: baseErr}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			if msg == "" {
				t.Error("Error() returned empty string")
			}
		})
	}
}

// TestErrorHierarchy_LifecycleErrorFromTransition verifies that Transition
// with an invalid state returns errors satisfying both LifecycleError and
// SpecError type assertions.
// Test Spec: TS-01-50, TS-01-51, Requirement: 01-REQ-26.1, 01-REQ-26.2
func TestErrorHierarchy_LifecycleErrorFromTransition(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
	}

	// draft → sealed is not a valid transition
	_, err := spec.Transition("sealed", tmpDir)
	if err == nil {
		t.Fatal("expected error for invalid transition, got nil")
	}

	var lifecycleErr *LifecycleError
	if !errors.As(err, &lifecycleErr) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T", err)
	}
}
