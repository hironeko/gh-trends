package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func testDashboardReport() *dashboardReport {
	comparison := dashboardMetric{PRs: 8, Releases: 1, LeadTime: "3h 0m"}
	return &dashboardReport{
		Repository: "owner/repo", Author: "octocat", Year: 2026, ComparisonYear: 2025,
		GeneratedAt: "2026-08-13T00:00:00Z",
		Rows:        []dashboardRow{{Month: 1, Primary: dashboardMetric{PRs: 10, Releases: 2, LeadTime: "2h 0m"}, Comparison: &comparison}},
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
