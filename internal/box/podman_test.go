package box

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

// onGOOS makes the messages of the podman provider those of the operating system.
func onGOOS(t *testing.T, goos string) {
	t.Helper()
	old := GOOS
	t.Cleanup(func() { GOOS = old })
	GOOS = goos
}

func TestExplainPodman_AnEngineThatCannotBeReachedIsNotRetriedAndNamesSandbox(t *testing.T) {
	for goos, machine := range map[string]bool{"darwin": true, "windows": true, "linux": false} {
		onGOOS(t, goos)

		f := asFailure(t, explainPodman(Request{}, errors.New("cannot connect to Podman socket")))

		if f.Category != toolio.CategorySandbox || f.Retryable || !strings.Contains(f.Message, "the podman engine cannot be reached: cannot connect to Podman socket") {
			t.Errorf("%s: %+v, want a sandbox failure that is not retried, with the error", goos, f)
		}
		if strings.Contains(f.Message, "podman machine start") != machine || f.FixHint == nil || *f.FixHint != (toolio.FixHint{Flag: "--sandbox"}) {
			t.Errorf("%s: %q with hint %+v, want `podman machine start` %v and --sandbox", goos, f.Message, f.FixHint, machine)
		}
	}
}

func TestExplainPodman_AnImageThatCannotBePulledIsRetriedAndNamesAFSandboxImage(t *testing.T) {
	onGOOS(t, "darwin")
	for _, message := range []string{
		"Error: example.test/img:1: image not known",
		"image not found",
		"Error: no such image: example.test/img:1",
		"Error: reading manifest latest in example.test/img: manifest unknown",
		"Trying to pull example.test/img:1... Error: name unknown",
		"Error: Pull access denied for example.test/img",
		"Error: unable to pull example.test/img:1: x509: certificate signed by unknown authority",
		"Error: failed to pull example.test/img:1: connection reset by peer",
		`Error: error pulling "example.test/img:1": EOF`,
		"denied: repository does not exist or may require authorization",
		"Error: initializing source docker://example.test/img:1: no such host",
	} {
		f := asFailure(t, explainPodman(Request{Image: "example.test/img:1"}, errors.New(message)))

		if f.Category != toolio.CategorySandbox || !f.Retryable || !strings.Contains(f.Message, "the image example.test/img:1 is missing") || f.FixHint == nil || f.FixHint.Env != "AF_SANDBOX_IMAGE" || strings.Contains(f.Message, "podman machine start") {
			t.Errorf("%q: %+v, want a retried sandbox failure of the image that names AF_SANDBOX_IMAGE", message, f)
		}
	}
	if f := asFailure(t, explainPodman(Request{}, errors.New("image not known"))); !strings.Contains(f.Message, "the image of the sandbox is missing") {
		t.Errorf("no image named: %q, want the sandbox's image", f.Message)
	}
}

func TestExplainPodman_AnImageWithNoAgentIsAFailureOfTheImage(t *testing.T) {
	onGOOS(t, "darwin")
	for image, says := range map[string]string{"example.test/img:1": "the image example.test/img:1 has no agent", "": "the image of the sandbox has no agent"} {
		f := asFailure(t, explainPodman(Request{Image: image}, fmt.Errorf("podman: create: %w", sandbox.ErrNoAgent)))

		if f.Category != toolio.CategorySandbox || f.Retryable || !strings.Contains(f.Message, says) || f.FixHint == nil || f.FixHint.Env != "AF_SANDBOX_IMAGE" || strings.Contains(f.Message, "cannot be reached") {
			t.Errorf("%+v, want a failure of the image that says %q", f, says)
		}
	}
}

func TestExplainPodman_AStoppedRunIsTheErrorOfItsContext(t *testing.T) {
	for _, ctxErr := range []error{context.Canceled, context.DeadlineExceeded} {
		err := fmt.Errorf("podman: create: %w", ctxErr)

		if got := explainPodman(Request{}, err); got != err {
			t.Errorf("%v: explainPodman = %v, want the error as it is", ctxErr, got)
		}
	}
}

func TestOpen_ThePodmanProviderOfDefaultExplainsItsFailures(t *testing.T) {
	dir := hostRepo(t, nil)
	onGOOS(t, "linux")
	old := newPodmanProvider
	t.Cleanup(func() { newPodmanProvider = old })
	for _, tc := range []struct {
		name      string
		make      func() (sandbox.Provider, error)
		says      string
		retryable bool
	}{
		{"no podman on PATH", func() (sandbox.Provider, error) {
			return nil, errors.New(`exec: "podman": executable file not found in $PATH`)
		}, "the podman engine cannot be reached: exec", false},
		{"an image that is not there", func() (sandbox.Provider, error) {
			return &fakeProvider{Provider: localEntry().New(), f: &fake{createErr: errors.New("image not known")}}, nil
		}, "is missing and cannot be pulled", true},
	} {
		newPodmanProvider = tc.make
		req := request("podman", dir, nil, &ledger{})
		req.Image = "example.test/img:1"

		_, err := Open(context.Background(), req)

		if f := asFailure(t, err); f.Category != toolio.CategorySandbox || f.Retryable != tc.retryable || !strings.Contains(f.Message, tc.says) {
			t.Errorf("%s: %+v, want a sandbox failure saying %q, retryable %v", tc.name, f, tc.says, tc.retryable)
		}
	}
}
