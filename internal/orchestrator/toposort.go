package orchestrator

import (
	"encoding/json"
	"fmt"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// TopoSort returns task IDs in topological order using Kahn's algorithm.
// Tasks with no dependencies come first; dependents come after their blockers.
// Returns an error if a cycle is detected.
func TopoSort(tasks []db.Task) ([]string, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	// Build adjacency list and in-degree map
	taskMap := make(map[string]bool, len(tasks))
	inDegree := make(map[string]int, len(tasks))
	dependents := make(map[string][]string) // blocker -> list of tasks that depend on it

	for _, t := range tasks {
		taskMap[t.ID] = true
		inDegree[t.ID] = 0
	}

	for _, t := range tasks {
		blockers := parseBlockedBy(t.BlockedBy.String)
		for _, b := range blockers {
			if !taskMap[b] {
				continue // blocker not in this set, skip
			}
			dependents[b] = append(dependents[b], t.ID)
			inDegree[t.ID]++
		}
	}

	// Seed queue with tasks that have no incoming edges
	var queue []string
	for _, t := range tasks {
		if inDegree[t.ID] == 0 {
			queue = append(queue, t.ID)
		}
	}

	var sorted []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		sorted = append(sorted, id)

		for _, dep := range dependents[id] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(sorted) != len(tasks) {
		return nil, fmt.Errorf("cycle detected: sorted %d of %d tasks", len(sorted), len(tasks))
	}

	return sorted, nil
}

// parseBlockedBy extracts task IDs from a JSON array string like `["T-001","T-002"]`.
// Returns nil for empty or invalid input.
func parseBlockedBy(raw string) []string {
	if raw == "" || raw == "[]" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil
	}
	return ids
}
