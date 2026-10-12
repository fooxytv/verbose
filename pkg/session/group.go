package session

import "sort"

// Grouping a session list so a subagent sits under the parent that spawned it.
//
// The list is otherwise ordered by last-update time alone, which scatters a
// session's agents among unrelated work: the poc session that prompted this had
// two agents carrying 78% of its replay steps and 193 of its 194 file changes,
// shown as three rows with nothing but a `◦` to connect them.

// GroupSubagents reorders infos so each subagent immediately follows its
// parent, preserving the order of everything else.
//
// A subagent whose parent is not in the list keeps its own place. That happens
// whenever the list is filtered to a project the parent is not in, or the
// parent's transcript has been deleted — and dropping it, or hoisting it to the
// end, would hide a run that still has its own cost and its own work.
func GroupSubagents(infos []SessionInfo) []SessionInfo {
	if len(infos) < 2 {
		return infos
	}

	present := make(map[string]bool, len(infos))
	for _, s := range infos {
		present[s.ID] = true
	}

	// Children are keyed by parent, in the order the agents finished, so a
	// parent's runs read as the sequence of work rather than the list's
	// newest-first order.
	children := make(map[string][]SessionInfo)
	for _, s := range infos {
		if s.IsAgent && s.ParentSessionID != "" && present[s.ParentSessionID] {
			children[s.ParentSessionID] = append(children[s.ParentSessionID], s)
		}
	}
	for id := range children {
		kids := children[id]
		sort.SliceStable(kids, func(i, j int) bool {
			return kids[i].LastUpdate.Before(kids[j].LastUpdate)
		})
	}

	out := make([]SessionInfo, 0, len(infos))
	for _, s := range infos {
		// Emitted under its parent rather than here.
		if s.IsAgent && s.ParentSessionID != "" && present[s.ParentSessionID] {
			continue
		}
		out = append(out, s)
		out = append(out, children[s.ID]...)
	}
	return out
}

// IsNestedUnder reports whether infos[i] is drawn as a child of the row above
// it: a subagent whose parent is somewhere in the same list.
//
// An orphan is not nested, because there is no parent row for it to sit under.
func IsNestedUnder(infos []SessionInfo, i int) bool {
	if i < 0 || i >= len(infos) {
		return false
	}
	s := infos[i]
	if !s.IsAgent || s.ParentSessionID == "" {
		return false
	}
	for _, other := range infos {
		if other.ID == s.ParentSessionID {
			return true
		}
	}
	return false
}

// IsLastChild reports whether infos[i] is the final subagent of its parent, so
// the tree connector can close with └ rather than ├.
func IsLastChild(infos []SessionInfo, i int) bool {
	if !IsNestedUnder(infos, i) {
		return false
	}
	parent := infos[i].ParentSessionID
	for j := i + 1; j < len(infos); j++ {
		if infos[j].IsAgent && infos[j].ParentSessionID == parent {
			return false
		}
		// The run of children ends at the next row belonging to anything else.
		break
	}
	return true
}
