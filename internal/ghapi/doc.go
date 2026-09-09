// Package ghapi is a small, dependency-free GitHub REST client for the
// agent-fox tools.
//
// It exists rather than shelling out to the `gh` CLI for three reasons. A
// tool that runs unattended in a container cannot rely on a binary being
// installed and interactively authenticated. A subprocess boundary turns
// every API error into a string that has to be pattern-matched, where an
// HTTP status is a number. And a client behind an interface can be pointed
// at an httptest server, so the tools' GitHub behaviour is covered by tests
// that make no network call.
//
// It also lives outside every agent in this repository on purpose. No model
// is given a tool that reaches this package: the calls are made by the
// pipelines, before and after a run, at points that know why. The
// consequence is worth stating plainly — no sequence of model outputs can
// cause an agent-fox tool to write to GitHub.
package ghapi
