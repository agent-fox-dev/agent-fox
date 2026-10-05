// Package fixture is a syntactic fixture for the Run.Warn source scan
// (TS-05-36): it is not built or imported by anything, only parsed by path.
package fixture

// Run stands in for toolio.Run, just enough shape for the scan to find a
// Warn call site.
type Run struct{}

// WarnCode stands in for toolio.WarnCode.
type WarnCode string

// Warn stands in for toolio.Run.Warn.
func (r *Run) Warn(code any, severity string, format string, args ...any) {}

func doSomething(r *Run) {
	// This is the one violation the scan must catch: a bare string literal
	// where a declared WarnCode constant belongs.
	r.Warn("some_code", "high", "message")

	// And the other way round: a literal converted to WarnCode type-checks
	// against a WarnCode parameter just as well, and is no more declared.
	r.Warn(WarnCode("undeclared"), "high", "message")
}
