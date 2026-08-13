package actions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/hironeko/gh-trends/internal/animation"
	"os/exec"
	"sync"
	"time"
)

// WorkflowRun represents a GitHub Actions workflow run
type WorkflowRun struct {
	Attempt      int       `json:"attempt"`
	Conclusion   string    `json:"conclusion"`
	CreatedAt    time.Time `json:"createdAt"`
	DatabaseId   int64     `json:"databaseId"`
	DisplayTitle string    `json:"displayTitle"`
	Event        string    `json:"event"`
	HeadBranch   string    `json:"headBranch"`
	Name         string    `json:"name"`
	Number       int       `json:"number"`
	StartedAt    time.Time `json:"startedAt"`
	Status       string    `json:"status"`
	UpdatedAt    time.Time `json:"updatedAt"`
	WorkflowName string    `json:"workflowName"`
	URL          string    `json:"url"`
}

// WorkflowJob represents a job within a workflow run
type WorkflowJob struct {
	CompletedAt time.Time      `json:"completedAt"`
	Conclusion  string         `json:"conclusion"`
	DatabaseId  int64          `json:"databaseId"`
	Name        string         `json:"name"`
	StartedAt   time.Time      `json:"startedAt"`
	Status      string         `json:"status"`
	Steps       []WorkflowStep `json:"steps"`
	URL         string         `json:"url"`
}

// WorkflowStep represents a step within a job
type WorkflowStep struct {
	CompletedAt time.Time `json:"completedAt"`
	Conclusion  string    `json:"conclusion"`
	Name        string    `json:"name"`
	Number      int       `json:"number"`
	StartedAt   time.Time `json:"startedAt"`
	Status      string    `json:"status"`
}

// WorkflowStats represents statistics for a specific workflow
type WorkflowStats struct {
	TotalRuns         int
	Successes         int
	Failures          int
	AverageDurationMs int64
}

// EventStats represents statistics for a specific trigger event
type EventStats struct {
	TotalRuns int
	Successes int
	Failures  int
}

// FailureDetail represents detailed information about a failure
type FailureDetail struct {
	WorkflowName string
	DisplayTitle string
	CreatedAt    time.Time
	Duration     time.Duration
	FailedJob    string
	FailedStep   string
	URL          string
}

// WorkflowAnalytics represents the complete analysis results
type WorkflowAnalytics struct {
	TotalRuns         int
	TotalSuccesses    int
	TotalFailures     int
	AverageDurationMs int64
	WorkflowStats     map[string]WorkflowStats
	EventStats        map[string]EventStats
	FailureDetails    []FailureDetail
}

// FetchWorkflowRuns fetches workflow runs from GitHub using gh CLI
func FetchWorkflowRuns(repo string, since, until string) ([]WorkflowRun, error) {
	args := []string{
		"run", "list",
		"--repo", repo,
		"--json", "attempt,conclusion,createdAt,databaseId,displayTitle,event,headBranch,name,number,startedAt,status,updatedAt,workflowName,url",
		"--limit", "500", // Fetch more runs for better analysis
	}

	// Note: gh run list doesn't support --created flag like pr list
	// Instead we'll filter the results after fetching
	// For now, we'll fetch recent runs and filter them in code

	// Start shiba animation for workflow runs
	spinner := animation.NewShibaSpinner("Fetching workflow runs...", false)
	spinner.Start()
	defer spinner.Stop()

	cmd := exec.Command("gh", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh command failed: %s\n%s", err, stderr.String())
	}

	var runs []WorkflowRun
	if err := json.Unmarshal(stdout.Bytes(), &runs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return runs, nil
}

// AnalyzeWorkflowRuns analyzes the fetched workflow runs
func AnalyzeWorkflowRuns(runs []WorkflowRun, since, until string) WorkflowAnalytics {
	// Filter runs by date range if provided
	var filteredRuns []WorkflowRun
	for _, run := range runs {
		include := true

		if since != "" {
			sinceDate, err := time.Parse("2006-01-02", since)
			if err == nil && run.CreatedAt.Before(sinceDate) {
				include = false
			}
		}

		if until != "" && include {
			untilDate, err := time.Parse("2006-01-02", until)
			if err == nil && run.CreatedAt.After(untilDate.AddDate(0, 0, 1)) { // Add 1 day to include the until date
				include = false
			}
		}

		if include {
			filteredRuns = append(filteredRuns, run)
		}
	}

	runs = filteredRuns
	analytics := WorkflowAnalytics{
		WorkflowStats:  make(map[string]WorkflowStats),
		EventStats:     make(map[string]EventStats),
		FailureDetails: make([]FailureDetail, 0),
	}

	var totalDuration time.Duration
	var completedRuns int

	for _, run := range runs {
		analytics.TotalRuns++

		// Calculate duration for completed runs
		if run.Status == "completed" && !run.StartedAt.IsZero() && !run.UpdatedAt.IsZero() {
			duration := run.UpdatedAt.Sub(run.StartedAt)
			totalDuration += duration
			completedRuns++
		}

		// Count successes and failures
		if run.Conclusion == "success" {
			analytics.TotalSuccesses++
		} else if run.Conclusion == "failure" || run.Conclusion == "cancelled" || run.Conclusion == "timed_out" {
			analytics.TotalFailures++

			// Add to failure details
			failureDetail := FailureDetail{
				WorkflowName: run.WorkflowName,
				DisplayTitle: run.DisplayTitle,
				CreatedAt:    run.CreatedAt,
				URL:          run.URL,
			}

			if !run.StartedAt.IsZero() && !run.UpdatedAt.IsZero() {
				failureDetail.Duration = run.UpdatedAt.Sub(run.StartedAt)
			}

			analytics.FailureDetails = append(analytics.FailureDetails, failureDetail)
		}

		// Update workflow statistics
		workflowStats := analytics.WorkflowStats[run.WorkflowName]
		workflowStats.TotalRuns++

		if run.Conclusion == "success" {
			workflowStats.Successes++
		} else if run.Conclusion == "failure" || run.Conclusion == "cancelled" || run.Conclusion == "timed_out" {
			workflowStats.Failures++
		}

		if run.Status == "completed" && !run.StartedAt.IsZero() && !run.UpdatedAt.IsZero() {
			duration := run.UpdatedAt.Sub(run.StartedAt)
			// Update average duration (simple approach)
			workflowStats.AverageDurationMs = (workflowStats.AverageDurationMs + duration.Milliseconds()) / 2
		}

		analytics.WorkflowStats[run.WorkflowName] = workflowStats

		// Update event statistics
		eventStats := analytics.EventStats[run.Event]
		eventStats.TotalRuns++

		if run.Conclusion == "success" {
			eventStats.Successes++
		} else if run.Conclusion == "failure" || run.Conclusion == "cancelled" || run.Conclusion == "timed_out" {
			eventStats.Failures++
		}

		analytics.EventStats[run.Event] = eventStats
	}

	// Calculate average duration
	if completedRuns > 0 {
		analytics.AverageDurationMs = totalDuration.Milliseconds() / int64(completedRuns)
	}

	// Fetch detailed failure information for recent failures
	if len(analytics.FailureDetails) > 0 {
		analytics.FailureDetails = fetchFailureDetails(runs, analytics.FailureDetails)
	}

	return analytics
}

// fetchFailureDetails fetches detailed job and step information for failures
func fetchFailureDetails(runs []WorkflowRun, failures []FailureDetail) []FailureDetail {
	// Limit to first 5 failures for performance
	limit := 5
	if len(failures) < limit {
		limit = len(failures)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < limit; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// Find the corresponding run
			var targetRun *WorkflowRun
			for _, run := range runs {
				if run.DisplayTitle == failures[index].DisplayTitle &&
					run.WorkflowName == failures[index].WorkflowName {
					targetRun = &run
					break
				}
			}

			if targetRun == nil {
				return
			}

			// Fetch job details
			jobInfo := fetchJobDetails(targetRun.DatabaseId)

			mu.Lock()
			if jobInfo.FailedJob != "" {
				failures[index].FailedJob = jobInfo.FailedJob
			}
			if jobInfo.FailedStep != "" {
				failures[index].FailedStep = jobInfo.FailedStep
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()
	return failures
}

// JobInfo represents extracted job failure information
type JobInfo struct {
	FailedJob  string
	FailedStep string
}

// fetchJobDetails fetches job details for a specific run
func fetchJobDetails(runId int64) JobInfo {
	args := []string{
		"run", "view", fmt.Sprintf("%d", runId),
		"--json", "jobs",
	}

	cmd := exec.Command("gh", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Silently ignore errors for job details
		return JobInfo{}
	}

	var runDetails struct {
		Jobs []WorkflowJob `json:"jobs"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &runDetails); err != nil {
		return JobInfo{}
	}

	// Find failed job and step
	for _, job := range runDetails.Jobs {
		if job.Conclusion == "failure" || job.Conclusion == "cancelled" || job.Conclusion == "timed_out" {
			jobInfo := JobInfo{FailedJob: job.Name}

			// Find failed step
			for _, step := range job.Steps {
				if step.Conclusion == "failure" || step.Conclusion == "cancelled" || step.Conclusion == "timed_out" {
					jobInfo.FailedStep = step.Name
					break
				}
			}

			return jobInfo
		}
	}

	return JobInfo{}
}
