package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// run runs the binary's shell with args and returns what it wrote.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = newApp().Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}
