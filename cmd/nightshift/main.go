package main

import (
	"fmt"

	"github.com/agent-fox-dev/agentfox"
)

func main() {
	fmt.Println("Nightshift CLI", agentfox.Version, agentfox.Commit, agentfox.BuildTime)
}
