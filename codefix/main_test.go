package codefix

import (
	"os"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/envtest"
)

// The pipeline tests give a repository a forge's origin to detect the forge;
// git must never reach it or ask for a credential.
func TestMain(m *testing.M) {
	envtest.NoGitNetwork()
	os.Exit(m.Run())
}
