package afspec

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TS-03-5: EstimateTokens is exported and returns len(text) / 4
// Requirement: 03-REQ-1.5
// ---------------------------------------------------------------------------

func TestEstimateTokens_EightChars(t *testing.T) {
	defer requireImplemented(t)

	result := EstimateTokens("abcdefgh")
	if result != 2 {
		t.Errorf("EstimateTokens(\"abcdefgh\") = %d, want 2", result)
	}
}

func TestEstimateTokens_FourChars(t *testing.T) {
	defer requireImplemented(t)

	result := EstimateTokens("abcd")
	if result != 1 {
		t.Errorf("EstimateTokens(\"abcd\") = %d, want 1", result)
	}
}

func TestEstimateTokens_EmptyString(t *testing.T) {
	defer requireImplemented(t)

	result := EstimateTokens("")
	if result != 0 {
		t.Errorf("EstimateTokens(\"\") = %d, want 0", result)
	}
}

// ---------------------------------------------------------------------------
// 03-REQ-1.E1: Short strings (1-3 chars) return 0
// ---------------------------------------------------------------------------

func TestEstimateTokens_OneChar(t *testing.T) {
	defer requireImplemented(t)

	result := EstimateTokens("a")
	if result != 0 {
		t.Errorf("EstimateTokens(\"a\") = %d, want 0", result)
	}
}

func TestEstimateTokens_TwoChars(t *testing.T) {
	defer requireImplemented(t)

	result := EstimateTokens("ab")
	if result != 0 {
		t.Errorf("EstimateTokens(\"ab\") = %d, want 0", result)
	}
}

func TestEstimateTokens_ThreeChars(t *testing.T) {
	defer requireImplemented(t)

	result := EstimateTokens("abc")
	if result != 0 {
		t.Errorf("EstimateTokens(\"abc\") = %d, want 0", result)
	}
}

// ---------------------------------------------------------------------------
// 03-REQ-1.E2: Exactly 4 chars returns 1
// ---------------------------------------------------------------------------

func TestEstimateTokens_ExactlyFour(t *testing.T) {
	defer requireImplemented(t)

	result := EstimateTokens("wxyz")
	if result != 1 {
		t.Errorf("EstimateTokens(\"wxyz\") = %d, want 1", result)
	}
}

// ---------------------------------------------------------------------------
// 03-REQ-1.E3: Large string (1 million chars) returns correct result
// ---------------------------------------------------------------------------

func TestEstimateTokens_LargeString(t *testing.T) {
	defer requireImplemented(t)

	text := strings.Repeat("x", 1_000_000)
	result := EstimateTokens(text)
	if result != 250_000 {
		t.Errorf("EstimateTokens(1M chars) = %d, want 250000", result)
	}
}

// ---------------------------------------------------------------------------
// 03-PROP-1: Floor division identity for arbitrary string
// Validates: 03-REQ-1.1, 03-REQ-1.2, 03-REQ-1.3
// ---------------------------------------------------------------------------

func TestEstimateTokens_ArbitraryString(t *testing.T) {
	defer requireImplemented(t)

	text := "abcdefghijklmnopq" // 17 chars
	result := EstimateTokens(text)
	expected := len(text) / 4
	if result != expected {
		t.Errorf("EstimateTokens(%q) = %d, want %d", text, result, expected)
	}
}

// ---------------------------------------------------------------------------
// TS-03-23: Go afspec package exports RenderOption, WithMaxTokens, and
//           EstimateTokens with correct types; renderConfig is unexported
// Requirement: 03-REQ-7.1
// ---------------------------------------------------------------------------

func TestWithMaxTokens_ReturnsRenderOption(t *testing.T) {
	defer requireImplemented(t)

	// WithMaxTokens must return a RenderOption (compile-time check via
	// explicit type assignment).
	var opt RenderOption = WithMaxTokens(100)
	if opt == nil {
		t.Fatal("WithMaxTokens(100) returned nil RenderOption")
	}
}

func TestEstimateTokens_FourCharString_ReturnsOne(t *testing.T) {
	// TS-03-23 assertion: EstimateTokens("test") == 1
	defer requireImplemented(t)

	result := EstimateTokens("test")
	if result != 1 {
		t.Errorf("EstimateTokens(\"test\") = %d, want 1", result)
	}
}

func TestWithMaxTokens_AppliesOptionToConfig(t *testing.T) {
	defer requireImplemented(t)

	// Verify that WithMaxTokens creates a valid option that can be applied
	// to a renderConfig (internal check — we are in the same package).
	cfg := &renderConfig{}
	opt := WithMaxTokens(500)
	opt(cfg)
	if cfg.maxTokens != 500 {
		t.Errorf("WithMaxTokens(500) set maxTokens to %d, want 500", cfg.maxTokens)
	}
}

func TestWithMaxTokens_Zero_SetsZero(t *testing.T) {
	defer requireImplemented(t)

	cfg := &renderConfig{}
	opt := WithMaxTokens(0)
	opt(cfg)
	if cfg.maxTokens != 0 {
		t.Errorf("WithMaxTokens(0) set maxTokens to %d, want 0", cfg.maxTokens)
	}
}

func TestWithMaxTokens_Negative_SetsNegative(t *testing.T) {
	defer requireImplemented(t)

	cfg := &renderConfig{}
	opt := WithMaxTokens(-10)
	opt(cfg)
	if cfg.maxTokens != -10 {
		t.Errorf("WithMaxTokens(-10) set maxTokens to %d, want -10", cfg.maxTokens)
	}
}
