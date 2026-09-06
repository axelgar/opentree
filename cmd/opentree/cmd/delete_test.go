package cmd

import (
	"slices"
	"testing"

	"github.com/axelgar/opentree/pkg/state"
)

func TestMergedWorkspaces(t *testing.T) {
	all := []*state.Workspace{
		{Name: "feat/a", PRStatus: "merged"},
		{Name: "feat/b", PRStatus: "open"},
		{Name: "feat/c"},
		{Name: "feat/d", PRStatus: "merged"},
		{Name: "feat/e", PRStatus: "closed"},
	}
	got := mergedWorkspaces(all)
	if want := []string{"feat/a", "feat/d"}; !slices.Equal(got, want) {
		t.Errorf("mergedWorkspaces = %v, want %v — merged only, closed is not merged", got, want)
	}
	if got := mergedWorkspaces(nil); len(got) != 0 {
		t.Errorf("mergedWorkspaces(nil) = %v", got)
	}
}
