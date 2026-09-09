package main

import "github.com/agent-fox-dev/agentfox"

func main() {
	// The Makefile injects the build metadata into the agentfox package.
	// A direct -X main.version=... override still wins.
	if version == "dev" && agentfox.Version != "" {
		version = agentfox.Version
	}
	Execute()
}
