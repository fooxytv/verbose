package session

import (
	"testing"
	"time"
)

func at(min int) time.Time {
	return time.Date(2026, 10, 12, 10, min, 0, 0, time.UTC)
}

func ids(infos []SessionInfo) []string {
	out := make([]string, len(infos))
	for i, s := range infos {
		out[i] = s.ID
	}
	return out
}

func eq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// A subagent belongs under the parent that spawned it, wherever its own
// timestamp would otherwise put it.
func TestGroupSubagentsPullsChildrenUnderTheirParent(t *testing.T) {
	infos := []SessionInfo{
		{ID: "other-new", LastUpdate: at(50)},
		{ID: "agent-2", IsAgent: true, ParentSessionID: "parent", LastUpdate: at(40)},
		{ID: "parent", LastUpdate: at(30)},
		{ID: "agent-1", IsAgent: true, ParentSessionID: "parent", LastUpdate: at(20)},
		{ID: "other-old", LastUpdate: at(10)},
	}
	got := GroupSubagents(infos)

	// Children follow the parent, oldest first: a parent's runs read as the
	// sequence of work, not the list's newest-first order.
	eq(t, ids(got), "other-new", "parent", "agent-1", "agent-2", "other-old")
}

// Filtering to one project, or deleting a parent's transcript, leaves a
// subagent with nobody to sit under. It still has its own cost and its own
// work, so it keeps its place rather than being dropped or hoisted away.
func TestGroupSubagentsKeepsOrphansWhereTheyAre(t *testing.T) {
	infos := []SessionInfo{
		{ID: "a", LastUpdate: at(30)},
		{ID: "orphan", IsAgent: true, ParentSessionID: "not-in-this-list", LastUpdate: at(20)},
		{ID: "b", LastUpdate: at(10)},
	}
	eq(t, ids(GroupSubagents(infos)), "a", "orphan", "b")
}

func TestGroupSubagentsHandlesSeveralParents(t *testing.T) {
	infos := []SessionInfo{
		{ID: "p1", LastUpdate: at(50)},
		{ID: "p2", LastUpdate: at(40)},
		{ID: "p2-kid", IsAgent: true, ParentSessionID: "p2", LastUpdate: at(35)},
		{ID: "p1-kid", IsAgent: true, ParentSessionID: "p1", LastUpdate: at(30)},
	}
	eq(t, ids(GroupSubagents(infos)), "p1", "p1-kid", "p2", "p2-kid")
}

func TestGroupSubagentsLeavesShortListsAlone(t *testing.T) {
	eq(t, ids(GroupSubagents(nil)))
	one := []SessionInfo{{ID: "only"}}
	eq(t, ids(GroupSubagents(one)), "only")
}

// The connector closes with └ on the last child and ├ on the others, so a run
// of agents reads as one block.
func TestNestingPredicates(t *testing.T) {
	infos := GroupSubagents([]SessionInfo{
		{ID: "parent", LastUpdate: at(30)},
		{ID: "k1", IsAgent: true, ParentSessionID: "parent", LastUpdate: at(10)},
		{ID: "k2", IsAgent: true, ParentSessionID: "parent", LastUpdate: at(20)},
		{ID: "after", LastUpdate: at(5)},
		{ID: "orphan", IsAgent: true, ParentSessionID: "gone", LastUpdate: at(1)},
	})
	eq(t, ids(infos), "parent", "k1", "k2", "after", "orphan")

	for i, want := range []bool{false, true, true, false, false} {
		if got := IsNestedUnder(infos, i); got != want {
			t.Errorf("IsNestedUnder(%d) [%s] = %v, want %v", i, infos[i].ID, got, want)
		}
	}
	if IsLastChild(infos, 1) {
		t.Error("k1 is not the last child; it must use ├")
	}
	if !IsLastChild(infos, 2) {
		t.Error("k2 is the last child; it must use └")
	}

	// Out of range must not panic.
	IsNestedUnder(infos, -1)
	IsNestedUnder(infos, 99)
	IsLastChild(infos, 99)
}
