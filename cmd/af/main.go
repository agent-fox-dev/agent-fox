package main

import (
	"fmt"

	"github.com/agent-fox-dev/agentfox"
)

func main() {
	fmt.Println("af CLI", agentfox.Version, agentfox.Commit, agentfox.BuildTime)
}
