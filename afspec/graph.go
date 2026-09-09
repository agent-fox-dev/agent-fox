package afspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BuildDependencyGraph reads tasks.json for each spec in the provided
// SpecMeta slice, extracts cross-spec dependency declarations, and
// returns a DependencyGraph.
//
// If a tasks.json references a spec ID not present in the provided
// SpecMeta slice, the dangling reference is recorded as an error and
// returned alongside the partial graph.
func BuildDependencyGraph(metas []SpecMeta, root string) (*DependencyGraph, error) {
	// Build a set of known spec IDs for dangling reference detection.
	knownSpecs := make(map[string]bool, len(metas))
	for _, m := range metas {
		knownSpecs[m.SpecID] = true
	}

	var edges []DependencyEdge
	var errs []error

	for _, meta := range metas {
		tasksPath := filepath.Join(meta.Dir, "tasks.json")
		tasksData, err := os.ReadFile(tasksPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("cannot read tasks.json for spec %s: %w", meta.SpecID, err))
			continue
		}

		var tasks TasksV1Json
		if err := json.Unmarshal(tasksData, &tasks); err != nil {
			errs = append(errs, fmt.Errorf("cannot parse tasks.json for spec %s: %w", meta.SpecID, err))
			continue
		}

		for _, dep := range tasks.Dependencies {
			edge := DependencyEdge{
				FromSpec:     meta.SpecID,
				ToSpec:       dep.DependsOnSpec,
				FromGroup:    dep.FromGroup,
				ToGroup:      dep.ToGroup,
				Relationship: dep.Relationship,
			}
			edges = append(edges, edge)

			if !knownSpecs[dep.DependsOnSpec] {
				errs = append(errs, fmt.Errorf("spec %s references unknown spec %s", meta.SpecID, dep.DependsOnSpec))
			}
		}
	}

	if edges == nil {
		edges = []DependencyEdge{}
	}

	graph := &DependencyGraph{Edges: edges}

	if len(errs) > 0 {
		return graph, errors.Join(errs...)
	}
	return graph, nil
}

// Dependencies returns all edges where the given spec is the dependent
// (i.e., edges where FromSpec matches specID — the spec's own dependencies).
func (g *DependencyGraph) Dependencies(specID string) []DependencyEdge {
	var result []DependencyEdge
	for _, e := range g.Edges {
		if e.FromSpec == specID {
			result = append(result, e)
		}
	}
	return result
}

// Dependents returns all edges where other specs depend on the given spec
// (i.e., edges where ToSpec matches specID).
func (g *DependencyGraph) Dependents(specID string) []DependencyEdge {
	var result []DependencyEdge
	for _, e := range g.Edges {
		if e.ToSpec == specID {
			result = append(result, e)
		}
	}
	return result
}

// TopologicalSort returns spec IDs in topological order using Kahn's
// algorithm such that every spec appears before its dependents.
// Returns an error if a cycle is detected.
//
// Nodes are inferred from edges. An empty graph returns an empty slice.
func (g *DependencyGraph) TopologicalSort() ([]string, error) {
	// Collect all nodes from edges.
	nodeSet := make(map[string]bool)
	for _, e := range g.Edges {
		nodeSet[e.FromSpec] = true
		nodeSet[e.ToSpec] = true
	}

	if len(nodeSet) == 0 {
		return []string{}, nil
	}

	// Build adjacency list and in-degree count.
	// An edge FromSpec→ToSpec means FromSpec depends on ToSpec.
	// In topological order, ToSpec must come before FromSpec.
	// So the "comes-after" adjacency is: ToSpec → [FromSpec].
	adj := make(map[string][]string)
	inDegree := make(map[string]int)
	for node := range nodeSet {
		inDegree[node] = 0
	}
	for _, e := range g.Edges {
		adj[e.ToSpec] = append(adj[e.ToSpec], e.FromSpec)
		inDegree[e.FromSpec]++
	}

	// Queue all nodes with in-degree 0 (no dependencies).
	queue := make([]string, 0)
	for node := range nodeSet {
		if inDegree[node] == 0 {
			queue = append(queue, node)
		}
	}
	sort.Strings(queue) // deterministic processing order

	var order []string
	for len(queue) > 0 {
		// Take the lexicographically smallest node for determinism.
		node := queue[0]
		queue = queue[1:]
		order = append(order, node)

		for _, next := range adj[node] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
				sort.Strings(queue)
			}
		}
	}

	if len(order) != len(nodeSet) {
		// Cycle detected — identify the nodes still with positive in-degree.
		var cycleNodes []string
		for node := range nodeSet {
			if inDegree[node] > 0 {
				cycleNodes = append(cycleNodes, node)
			}
		}
		sort.Strings(cycleNodes)
		return nil, fmt.Errorf("dependency cycle detected involving specs: %s", strings.Join(cycleNodes, ", "))
	}

	return order, nil
}
