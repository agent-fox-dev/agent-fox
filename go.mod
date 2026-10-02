module github.com/agent-fox-dev/agentfox

go 1.26.5

require (
	github.com/goccy/go-yaml v1.19.2
	github.com/google/go-cmp v0.7.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2
)

require (
	cloud.google.com/go/compute/metadata v0.3.0 // indirect
	golang.org/x/oauth2 v0.37.0
)

require (
	github.com/agentfox/agentkit-go v0.0.0
	golang.org/x/text v0.39.0 // indirect
)

replace github.com/agentfox/agentkit-go => ../agentkit-go
