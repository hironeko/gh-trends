package stats

import (
	"github.com/hironeko/gh-trends/internal/github"
	"testing"
)

func TestCalculateStatsCountsOnlyRevertsToMaster(t *testing.T) {
	prs := []github.PullRequest{
		{Title: "Revert broken change", Merged: true, BaseRefName: "master"},
		{Title: "REVERT another change", Merged: true, BaseRefName: "MASTER"},
		{Title: "Revert change on main", Merged: true, BaseRefName: "main"},
		{Title: "Revert change on release", Merged: true, BaseRefName: "release/v1"},
		{Title: "Regular change", Merged: true, BaseRefName: "master"},
		{Title: "Revert unmerged change", Merged: false, BaseRefName: "master"},
	}

	got := CalculateStats(prs)
	if got.RevertLikeMerges != 2 {
		t.Fatalf("RevertLikeMerges = %d, want 2", got.RevertLikeMerges)
	}
}
