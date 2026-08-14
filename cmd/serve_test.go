package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hironeko/gh-trends/internal/github"
	"github.com/spf13/cobra"
)

func testDashboardReport() *dashboardReport {
	comparison := dashboardMetric{PRs: 8, Releases: 1, LeadTime: "3h 0m"}
	return &dashboardReport{
		Repository: "owner/repo", Author: "octocat", Year: 2026, ComparisonYear: 2025,
		PrimaryLabel: "2026", ComparisonLabel: "2025",
		GeneratedAt: "2026-08-13T00:00:00Z",
		Rows:        []dashboardRow{{Month: 1, Label: "2026/01", ComparisonLabel: "2025/01", Primary: dashboardMetric{PRs: 10, Releases: 2, LeadTime: "2h 0m"}, Comparison: &comparison}},
	}
}

func TestBuildDashboardPlanSingleMonthComparisons(t *testing.T) {
	now := time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name           string
		mode           string
		comparisonYear int
		want           time.Time
	}{
		{name: "previous month", mode: "previous-month", want: time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)},
		{name: "previous year", mode: "previous-year", comparisonYear: 2025, want: time.Date(2025, time.August, 1, 0, 0, 0, 0, time.UTC)},
		{name: "specified year", mode: "specified-year", comparisonYear: 2024, want: time.Date(2024, time.August, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := buildDashboardPlan(2026, true, 8, 1, test.mode, test.comparisonYear, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Primary) != 1 || len(plan.Comparison) != 1 {
				t.Fatalf("plan lengths = %d/%d, want 1/1", len(plan.Primary), len(plan.Comparison))
			}
			if !plan.Comparison[0].Date.Equal(test.want) {
				t.Fatalf("comparison = %s, want %s", plan.Comparison[0].Date, test.want)
			}
			if plan.Primary[0].PartialDay != 14 || plan.Comparison[0].PartialDay != 14 {
				t.Fatalf("partial days = %d/%d, want 14/14", plan.Primary[0].PartialDay, plan.Comparison[0].PartialDay)
			}
		})
	}
}

func TestBuildDashboardPlanMultipleMonths(t *testing.T) {
	now := time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)
	plan, err := buildDashboardPlan(2026, true, 8, 6, "specified-year", 2024, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Primary) != 6 || len(plan.Comparison) != 6 {
		t.Fatalf("plan lengths = %d/%d, want 6/6", len(plan.Primary), len(plan.Comparison))
	}
	if got := plan.Primary[0].Date.Format("2006-01"); got != "2026-03" {
		t.Fatalf("first primary month = %s, want 2026-03", got)
	}
	if got := plan.Comparison[0].Date.Format("2006-01"); got != "2024-03" {
		t.Fatalf("first comparison month = %s, want 2024-03", got)
	}
	if plan.PrimaryLabel != "2026年3月〜2026年8月" || plan.ComparisonLabel != "2024年3月〜2024年8月" {
		t.Fatalf("labels = %q/%q", plan.PrimaryLabel, plan.ComparisonLabel)
	}
}

func TestBuildDashboardPlanRejectsFutureMonth(t *testing.T) {
	now := time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)
	if _, err := buildDashboardPlan(2026, true, 9, 1, "", 0, now); err == nil {
		t.Fatal("future month should be rejected")
	}
}

func TestDailyPRCountsUsesCreatedDateWithinPeriod(t *testing.T) {
	prs := make([]github.PullRequest, 4)
	prs[0].CreatedAt = time.Date(2026, time.August, 2, 1, 0, 0, 0, time.UTC)
	prs[1].CreatedAt = time.Date(2026, time.August, 2, 23, 0, 0, 0, time.UTC)
	prs[2].CreatedAt = time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)
	prs[3].CreatedAt = time.Date(2026, time.July, 31, 23, 59, 0, 0, time.UTC)

	got := dailyPRCounts(
		prs,
		time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 14, 23, 59, 59, 0, time.UTC),
	)
	if got["2026-08-02"] != 2 || got["2026-08-14"] != 1 || len(got) != 2 {
		t.Fatalf("daily counts = %#v", got)
	}
}

func TestDashboardStatusAndMarkdown(t *testing.T) {
	report := testDashboardReport()
	state := &dashboardState{status: "complete", stage: "取得完了", completed: 2, total: 2, started: time.Now(), report: report}
	handler := dashboardMux(state)

	for _, path := range []string{"/", "/api/status", "/report.md"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, recorder.Code)
		}
	}

	markdown := markdownReport(report)
	for _, expected := range []string{"# RepoTrends Report", "`owner/repo`", "`@octocat`", "2026", "2025", "2h 0m"} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("markdown report does not contain %q", expected)
		}
	}
}

func TestMarkdownUnavailableWhileRunning(t *testing.T) {
	state := &dashboardState{status: "running", started: time.Now()}
	request := httptest.NewRequest(http.MethodGet, "/report.md", nil)
	recorder := httptest.NewRecorder()
	dashboardMux(state).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestServeInteractiveWhenNoSelectionFlags(t *testing.T) {
	command := &cobra.Command{Use: "serve"}
	command.Flags().String("repo", "", "")
	command.Flags().String("year", "", "")
	command.Flags().String("compare-year", "", "")
	command.Flags().Bool("compare-prev-year", false, "")
	command.Flags().Bool("compare-prev-month", false, "")
	command.Flags().Int("month", 0, "")
	command.Flags().Int("months", 1, "")
	command.Flags().Int("port", 8080, "")

	if !serveHasNoSelectionFlags(command) {
		t.Fatal("serve without selection flags should use interactive mode")
	}
	if err := command.Flags().Set("repo", "owner/repo"); err != nil {
		t.Fatal(err)
	}
	if serveHasNoSelectionFlags(command) {
		t.Fatal("serve with an explicit selection flag should remain non-interactive")
	}
}
