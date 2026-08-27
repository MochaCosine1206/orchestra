package recursion

// MaxAgents returns the maximum number of agents allowed at a given depth.
// depth 0 → 8, depth 1 → 3, depth 2+ → 1.
func MaxAgents(depth int) int {
	switch {
	case depth <= 0:
		return 8
	case depth == 1:
		return 3
	default:
		return 1
	}
}
