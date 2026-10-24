package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingReader is a stdin that cannot be read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("the pipe is broken") }

// afRunReader is afRun with a stdin that is any reader.
func afRunReader(t *testing.T, stdin io.Reader, args ...string) afResult {
	t.Helper()
	var out, errb bytes.Buffer
	code := newApp().Main(context.Background(), append([]string{"af"}, args...), stdin, &out, &errb)
	res := afResult{code: code, stdout: out.String(), stderr: errb.String()}
	if out.Len() > 0 {
		res.env = oneDocument(t, res.stdout)
	}
	return res
}

// tokenFile writes content to a file of its own and returns its path.
func tokenFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// rawRun runs the shell of the binary and returns what it wrote, which is text for the help.
func rawRun(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = newApp().Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}
