package cmd

import (
	"fmt"
	"github.com/hironeko/gh-trends/internal/actions"
	"github.com/hironeko/gh-trends/internal/git"
	"github.com/hironeko/gh-trends/internal/i18n"
	"os"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var actionsCmd = &cobra.Command{
	Use:   "actions",
	Short: "Analyze GitHub Actions CI/CD performance",
	Long:  `Analyze GitHub Actions workflows to provide insights on CI/CD performance, failure rates, and execution times.`,
	Run: func(cmd *cobra.Command, args []string) {
		runActionsAnalysis()
	},
}

func init() {
	rootCmd.AddCommand(actionsCmd)
	actionsCmd.Flags().StringVarP(&repo, "repo", "r", "", "GitHub repository in 'owner/repo' format")
	actionsCmd.Flags().StringVarP(&since, "since", "s", "", "Analyze runs since date (YYYY-MM-DD)")
	actionsCmd.Flags().StringVarP(&until, "until", "u", "", "Analyze runs until date (YYYY-MM-DD)")
}

func runActionsAnalysis() {
	fmt.Println(i18n.T("🔧 GitHub Actions Analysis"))
	fmt.Println("=" + strings.Repeat("=", 50))

	// Get repository
	targetRepo, err := getActionsRepo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	repo = targetRepo

	// Set default date range if not provided (last 1 month)
	if since == "" && until == "" {
		now := time.Now()
		since = now.AddDate(0, -1, 0).Format("2006-01-02")
		until = now.Format("2006-01-02")
		fmt.Printf(i18n.Sprintf("📅 Using default date range: %s to %s\n"), since, until)
	}

	fmt.Printf(i18n.Sprintf("✅ Analyzing repository: %s\n"), repo)
	fmt.Printf(i18n.Sprintf("📊 Period: %s to %s\n"), since, until)

	// Fetch workflow runs
	fmt.Println(i18n.T("🔄 Fetching workflow runs..."))
	runs, err := actions.FetchWorkflowRuns(repo, since, until)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching workflow runs: %v\n", err)
		os.Exit(1)
	}

	if len(runs) == 0 {
		fmt.Println(i18n.T("⚠️  No workflow runs found in the specified period"))
		return
	}

	// Analyze runs
	fmt.Printf("🎯 Found %d workflow runs\n", len(runs))
	analytics := actions.AnalyzeWorkflowRuns(runs, since, until)

	// Display results
	displayActionsAnalytics(analytics)

	// Optional: Show failure details
	if analytics.TotalFailures > 0 {
		showFailureDetails := promptui.Select{
			Label: "Show failure details?",
			Items: []string{"Yes", "No"},
		}
		_, result, err := showFailureDetails.Run()
		if err == nil && result == "Yes" {
			displayFailureDetails(analytics.FailureDetails)
		}
	}
}

func getActionsRepo() (string, error) {
	if repo != "" {
		return repo, nil
	}

	detectedRepo, err := git.GetRepoFromGitRemote()
	if err == nil {
		prompt := promptui.Select{
			Label: fmt.Sprintf("Found repository '%s'. Analyze this?", detectedRepo),
			Items: []string{"Yes", "No, enter manually"},
		}
		_, result, err := prompt.Run()
		if err != nil {
			return "", fmt.Errorf("prompt failed %w", err)
		}

		if result == "Yes" {
			return detectedRepo, nil
		}
	}

	// Manual entry
	prompt := promptui.Prompt{
		Label: "Enter GitHub repository (owner/repo format)",
		Validate: func(input string) error {
			if len(strings.Split(input, "/")) != 2 || strings.TrimSpace(input) == "" {
				return fmt.Errorf("invalid format, please use 'owner/repo'")
			}
			return nil
		},
	}
	result, err := prompt.Run()
	if err != nil {
		return "", fmt.Errorf("prompt failed %w", err)
	}
	return result, nil
}

func displayActionsAnalytics(analytics actions.WorkflowAnalytics) {
	fmt.Println("\n" + i18n.T("🎯 GitHub Actions Analytics"))
	fmt.Println("=" + strings.Repeat("=", 50))

	// Summary Statistics Table
	fmt.Println("\n" + i18n.T("📊 Summary Statistics:"))
	summaryTable := tablewriter.NewWriter(os.Stdout)
	summaryTable.SetHeader([]string{i18n.T("Metric"), i18n.T("Value")})
	summaryTable.SetBorder(true)

	successRate := float64(analytics.TotalSuccesses) / float64(analytics.TotalRuns) * 100
	avgDuration := time.Duration(analytics.AverageDurationMs) * time.Millisecond

	summaryTable.Append([]string{i18n.T("Total Runs"), fmt.Sprintf("%d", analytics.TotalRuns)})
	summaryTable.Append([]string{i18n.T("Successful Runs"), fmt.Sprintf("%d", analytics.TotalSuccesses)})
	summaryTable.Append([]string{i18n.T("Failed Runs"), fmt.Sprintf("%d", analytics.TotalFailures)})
	summaryTable.Append([]string{i18n.T("Success Rate"), fmt.Sprintf("%.1f%%", successRate)})
	summaryTable.Append([]string{i18n.T("Avg Duration"), formatDuration(avgDuration)})
	summaryTable.Render()

	// Workflow Breakdown Table
	if len(analytics.WorkflowStats) > 0 {
		fmt.Println("\n" + i18n.T("🔄 Workflow Breakdown:"))
		workflowTable := tablewriter.NewWriter(os.Stdout)
		workflowTable.SetHeader([]string{i18n.T("Workflow"), i18n.T("Runs"), i18n.T("Success"), i18n.T("Failed"), i18n.T("Success Rate"), i18n.T("Avg Duration")})
		workflowTable.SetBorder(true)

		for workflowName, stats := range analytics.WorkflowStats {
			workflowSuccessRate := float64(stats.Successes) / float64(stats.TotalRuns) * 100
			avgWorkflowDuration := time.Duration(stats.AverageDurationMs) * time.Millisecond

			workflowTable.Append([]string{
				workflowName,
				fmt.Sprintf("%d", stats.TotalRuns),
				fmt.Sprintf("%d", stats.Successes),
				fmt.Sprintf("%d", stats.Failures),
				fmt.Sprintf("%.1f%%", workflowSuccessRate),
				formatDuration(avgWorkflowDuration),
			})
		}
		workflowTable.Render()
	}

	// Event Trigger Analysis
	if len(analytics.EventStats) > 0 {
		fmt.Println("\n" + i18n.T("⚡ Trigger Event Analysis:"))
		eventTable := tablewriter.NewWriter(os.Stdout)
		eventTable.SetHeader([]string{i18n.T("Event"), i18n.T("Runs"), i18n.T("Success Rate")})
		eventTable.SetBorder(true)

		for event, stats := range analytics.EventStats {
			eventSuccessRate := float64(stats.Successes) / float64(stats.TotalRuns) * 100
			eventTable.Append([]string{
				event,
				fmt.Sprintf("%d", stats.TotalRuns),
				fmt.Sprintf("%.1f%%", eventSuccessRate),
			})
		}
		eventTable.Render()
	}
}

func displayFailureDetails(failures []actions.FailureDetail) {
	fmt.Println("\n" + i18n.T("❌ Failure Analysis:"))
	fmt.Println("=" + strings.Repeat("=", 50))

	for i, failure := range failures {
		if i >= 10 { // Limit to first 10 failures
			fmt.Printf(i18n.Sprintf("\n... and %d more failures\n", len(failures)-10))
			break
		}

		fmt.Printf("\n" + i18n.Sprintf("🔴 Failure #%d:", i+1) + "\n")
		fmt.Printf(i18n.Sprintf("  Workflow: %s\n", failure.WorkflowName))
		fmt.Printf(i18n.Sprintf("  Run: %s\n", failure.DisplayTitle))
		fmt.Printf(i18n.Sprintf("  Date: %s\n", failure.CreatedAt.Format("2006-01-02 15:04")))
		fmt.Printf(i18n.Sprintf("  Duration: %s\n", formatDuration(failure.Duration)))

		if failure.FailedJob != "" {
			fmt.Printf(i18n.Sprintf("  Failed Job: %s\n", failure.FailedJob))
		}
		if failure.FailedStep != "" {
			fmt.Printf(i18n.Sprintf("  Failed Step: %s\n", failure.FailedStep))
		}
		fmt.Printf(i18n.Sprintf("  URL: %s\n", failure.URL))
	}
}
