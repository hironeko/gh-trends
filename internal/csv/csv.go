package csv

import (
	"encoding/csv"
	"fmt"
	"github.com/hironeko/gh-trends/internal/github"
	"os"
	"time"
)

// WritePullRequestsToCSV writes a slice of PullRequests to a CSV file.
func WritePullRequestsToCSV(filename string, prs []github.PullRequest) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write CSV header
	header := []string{
		"Number", "Title", "CreatedAt", "MergedAt", "ClosedAt", "Merged", "LeadTime (Hours)",
		"Author", "Additions", "Deletions", "ChangedFiles", "Commits",
		"IsDraft", "State", "MergedBy",
	}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write PR data
	for _, pr := range prs {
		leadTimeHours := pr.LeadTime.Hours()
		record := []string{
			fmt.Sprintf("%d", pr.Number),
			pr.Title,
			pr.CreatedAt.Format(time.RFC3339),
			pr.MergedAt.Format(time.RFC3339),
			pr.ClosedAt.Format(time.RFC3339),
			fmt.Sprintf("%t", pr.Merged),
			fmt.Sprintf("%.2f", leadTimeHours),
			pr.Author.Login,
			fmt.Sprintf("%d", pr.Additions),
			fmt.Sprintf("%d", pr.Deletions),
			fmt.Sprintf("%d", pr.ChangedFiles),
			"0", // Commits disabled due to GraphQL complexity
			fmt.Sprintf("%t", pr.IsDraft),
			pr.State,
			pr.MergedBy.Login,
		}
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("failed to write CSV record: %w", err)
		}
	}

	return nil
}
