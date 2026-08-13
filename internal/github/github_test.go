package github

import (
	"slices"
	"testing"
)

func TestFilterDependabotPRs(t *testing.T) {
	dependabot := PullRequest{Number: 1, Title: "Bump a dependency"}
	dependabot.Author.Login = "dependabot[bot]"

	dependabotPreview := PullRequest{Number: 2, Title: "Update a dependency"}
	dependabotPreview.Author.Login = "dependabot-preview[bot]"

	dependabotBranch := PullRequest{Number: 3, Title: "Bump a dependency", HeadRefName: "dependabot/go_modules/example-1.2.3"}

	otherBot := PullRequest{Number: 4, Title: "Automated maintenance"}
	otherBot.Author.Login = "renovate[bot]"

	humanMentioningDependabot := PullRequest{Number: 5, Title: "Configure Dependabot"}
	humanMentioningDependabot.Author.Login = "octocat"

	got := filterDependabotPRs([]PullRequest{
		dependabot,
		dependabotPreview,
		dependabotBranch,
		otherBot,
		humanMentioningDependabot,
	})

	if len(got) != 2 {
		t.Fatalf("filterDependabotPRs returned %d PRs, want 2", len(got))
	}
	if got[0].Number != otherBot.Number || got[1].Number != humanMentioningDependabot.Number {
		t.Fatalf("filterDependabotPRs returned PRs %d and %d, want %d and %d", got[0].Number, got[1].Number, otherBot.Number, humanMentioningDependabot.Number)
	}
}

func TestBuildMergedPRArgsIncludesFilters(t *testing.T) {
	args := buildMergedPRArgs("owner/repo", "2026-01-01", "2026-01-31", "octocat", "bug")
	wantPairs := [][2]string{{"--author", "octocat"}, {"--label", "bug"}}
	for _, pair := range wantPairs {
		index := slices.Index(args, pair[0])
		if index < 0 || index+1 >= len(args) || args[index+1] != pair[1] {
			t.Fatalf("arguments %v do not contain %s %s", args, pair[0], pair[1])
		}
	}
}
