module github.com/agent-fox-dev/agentfox

go 1.26.5

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/goccy/go-yaml v1.19.2
	github.com/google/go-cmp v0.7.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2
	github.com/spf13/cobra v1.10.2
	golang.org/x/sync v0.22.0
)

require (
	github.com/agentfox/agentkit-go v0.0.0
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/text v0.14.0 // indirect
)

replace github.com/agentfox/agentkit-go => ../coder
