package afspec

import "testing"

// ---------------------------------------------------------------------------
// Subtask 3.3: IsSpecDirName and ParseSpecDirName
// ---------------------------------------------------------------------------

// TestIsSpecDirName_ValidNames verifies that IsSpecDirName returns true for
// strings matching the NN_snake_case pattern.
// Test Spec: TS-01-27, Requirement: 01-REQ-14.1
func TestIsSpecDirName_ValidNames(t *testing.T) {
	defer requireImplemented(t)

	tests := []struct {
		name string
		want bool
	}{
		{"01_my_spec", true},
		{"99_another_spec", true},
		{"10_foo_bar_baz", true},
		{"42_simple", true},
		{"01_a", true},
		{"001_foo", true},
		{"100_my_spec", true},
		{"999_edge_case", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer requireImplemented(t)
			got := IsSpecDirName(tt.name)
			if got != tt.want {
				t.Errorf("IsSpecDirName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestIsSpecDirName_InvalidNames verifies that IsSpecDirName returns false for
// strings that do not match the NN_snake_case pattern.
// Test Spec: TS-01-28, Requirement: 01-REQ-14.2
func TestIsSpecDirName_InvalidNames(t *testing.T) {
	defer requireImplemented(t)

	tests := []struct {
		name string
		want bool
	}{
		{"my_spec", false},    // no numeric prefix
		{"01-my-spec", false}, // hyphens instead of underscores
		{"1_spec", false},     // single digit prefix
		{"abc_spec", false},   // non-numeric prefix
		{"01_", false},        // no name after underscore
		{"01", false},         // no underscore or name
		{"01_Foo", false},     // uppercase letters
		{"01_foo-bar", false}, // hyphen in name
		{"01_foo bar", false}, // space in name
		{"_01_foo", false},    // leading underscore
		{"01__foo", false},    // double underscore
	}

	for _, tt := range tests {
		t.Run(tt.name+"→false", func(t *testing.T) {
			defer requireImplemented(t)
			got := IsSpecDirName(tt.name)
			if got != tt.want {
				t.Errorf("IsSpecDirName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestIsSpecDirName_EmptyString verifies that IsSpecDirName returns false for
// an empty string.
// Requirement: 01-REQ-14.E2
func TestIsSpecDirName_EmptyString(t *testing.T) {
	defer requireImplemented(t)

	got := IsSpecDirName("")
	if got {
		t.Errorf("IsSpecDirName(%q) = true, want false", "")
	}
}

// TestParseSpecDirName_ValidName verifies that ParseSpecDirName returns the
// numeric prefix and snake_case name portion for a valid NN_snake_case name.
// Test Spec: TS-01-29, Requirement: 01-REQ-14.3
func TestParseSpecDirName_ValidName(t *testing.T) {
	defer requireImplemented(t)

	tests := []struct {
		input    string
		wantNum  string
		wantName string
	}{
		{"01_my_spec", "01", "my_spec"},
		{"99_another_spec", "99", "another_spec"},
		{"10_foo_bar_baz", "10", "foo_bar_baz"},
		{"42_simple", "42", "simple"},
		{"100_my_spec", "100", "my_spec"},
		{"001_foo", "001", "foo"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			defer requireImplemented(t)
			num, name, err := ParseSpecDirName(tt.input)
			if err != nil {
				t.Fatalf("ParseSpecDirName(%q) returned unexpected error: %v", tt.input, err)
			}
			if num != tt.wantNum {
				t.Errorf("ParseSpecDirName(%q) num = %q, want %q", tt.input, num, tt.wantNum)
			}
			if name != tt.wantName {
				t.Errorf("ParseSpecDirName(%q) name = %q, want %q", tt.input, name, tt.wantName)
			}
		})
	}
}

// TestParseSpecDirName_InvalidName verifies that ParseSpecDirName returns an
// error for strings that do not match the NN_snake_case pattern.
// Requirement: 01-REQ-14.E1
func TestParseSpecDirName_InvalidName(t *testing.T) {
	defer requireImplemented(t)

	invalids := []string{
		"my_spec",
		"01-my-spec",
		"1_spec",
		"abc_spec",
		"",
		"01_",
	}

	for _, input := range invalids {
		t.Run("invalid_"+input, func(t *testing.T) {
			defer requireImplemented(t)
			num, name, err := ParseSpecDirName(input)
			if err == nil {
				t.Errorf("ParseSpecDirName(%q) returned nil error, want error; got num=%q, name=%q", input, num, name)
			}
			if num != "" {
				t.Errorf("ParseSpecDirName(%q) num = %q, want empty string on error", input, num)
			}
			if name != "" {
				t.Errorf("ParseSpecDirName(%q) name = %q, want empty string on error", input, name)
			}
		})
	}
}
