package findings

// TopN returns at most n findings per rule, preserving the existing order.
// Since Deduplicate sorts by severity then count, the first n per rule are
// the worst n.  If n <= 0, all findings are returned unchanged.
func TopN(fs []Finding, n int) []Finding {
	if n <= 0 {
		return fs
	}
	counts := make(map[string]int)
	result := make([]Finding, 0, len(fs))
	for _, f := range fs {
		if counts[f.Rule] < n {
			result = append(result, f)
			counts[f.Rule]++
		}
	}
	return result
}
