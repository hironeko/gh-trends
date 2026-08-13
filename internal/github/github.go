package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/hironeko/gh-trends/internal/animation"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// PullRequest represents a GitHub Pull Request.
type PullRequest struct {
	Number    int           `json:"number"`
	Title     string        `json:"title"`
	CreatedAt time.Time     `json:"createdAt"`
	MergedAt  time.Time     `json:"mergedAt"`
	ClosedAt  time.Time     `json:"closedAt"`
	Merged    bool          `json:"merged"`
	LeadTime  time.Duration // Calculated field

	// Additional fields from gh pr list --json
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	ChangedFiles int `json:"changedFiles"`
	Commits      []struct {
		CommittedDate time.Time `json:"committedDate"`
	} `json:"commits"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Reviews []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		SubmittedAt time.Time `json:"submittedAt"`
		State       string    `json:"state"`
	} `json:"reviews"`
	Comments struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
	MergeCommit struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
	BaseRefName      string `json:"baseRefName"`
	IsDraft          bool   `json:"isDraft"`
	State            string `json:"state"`            // e.g., "OPEN", "CLOSED", "MERGED"
	Mergeable        string `json:"mergeable"`        // e.g., "MERGEABLE", "CONFLICTING", "UNKNOWN"
	MergeStateStatus string `json:"mergeStateStatus"` // e.g., "BEHIND", "BLOCKED", "CLEAN", "DIRTY", "DRAFT", "HAS_CONFLICTS", "UNKNOWN", "UNSTABLE"
	ReviewDecision   string `json:"reviewDecision"`   // e.g., "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED"
	MergedBy         struct {
		Login string `json:"login"`
	} `json:"mergedBy"`
	HeadRefName string `json:"headRefName"`

	// Comment timing metrics (calculated fields)
	FirstCommentTime      time.Time     `json:"-"` // Time of first comment
	FirstReviewTime       time.Time     `json:"-"` // Time of first review
	TimeToFirstComment    time.Duration `json:"-"` // Time from creation to first comment
	TimeToFirstReview     time.Duration `json:"-"` // Time from creation to first review
	AvgReviewResponseTime time.Duration `json:"-"` // Average response time to reviews

	// Comment quantity metrics (calculated fields)
	CommentCount       int `json:"-"` // Total number of comments on PR
	ReviewCommentCount int `json:"-"` // Total number of review comments (code comments, excluding replies)

	// Lifecycle metrics
	IsReopened      bool      `json:"-"`
	FirstReopenedAt time.Time `json:"-"`
}

// ValidateRepository verifies that the repository exists and is accessible to
// the account currently authenticated with GitHub CLI.
func ValidateRepository(repo string) error {
	cmd := exec.Command("gh", "repo", "view", repo, "--json", "nameWithOwner")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("repository %q was not found or is not accessible: %s", repo, detail)
	}
	return nil
}

// FetchPullRequests fetches pull requests from GitHub using gh pr list command with time-based parallel fetching.
func FetchPullRequests(repo string, since, until, author, label string, includeOpen bool) ([]PullRequest, error) {
	// If no date range is specified, use a simple single request
	if since == "" && until == "" {
		return fetchPRsSingle(repo, since, until, author, label, includeOpen)
	}

	// For date ranges, try to split into smaller chunks for parallel processing
	prs, err := fetchPRsWithDateSplit(repo, since, until, author, label, includeOpen)
	if err != nil {
		return nil, err
	}

	// Supplement: fetch merged PRs within date range (mergedAt) to avoid missing old-created PRs
	if since != "" && until != "" {
		mergedPRs, err := fetchMergedPRsByRange(repo, since, until, author, label)
		if err == nil && len(mergedPRs) > 0 {
			prs = append(prs, mergedPRs...)
		}
	}

	// Apply the exclusion after combining every fetch path so Dependabot PRs
	// supplemented by mergedAt cannot be reintroduced.
	return filterDependabotPRs(deduplicatePRs(prs)), nil
}

// fetchPRsSingle fetches PRs with a single request (for no date filtering)
func fetchPRsSingle(repo string, since, until, author, label string, includeOpen bool) ([]PullRequest, error) {
	args := buildBaseArgs(repo, since, until, author, label, includeOpen)
	args = append(args, "--limit", "1000") // Maximum limit

	// Start shiba animation (simple emoji version)
	spinner := animation.NewShibaSpinner("Fetching PRs...", false)
	spinner.Start()
	defer spinner.Stop()

	var prs []PullRequest
	var lastErr error

	for attempt := 1; attempt <= 3; attempt++ {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command("gh", args...)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("gh command failed: %s\n%s", err, stderr.String())
			// Retry transient upstream issues like 504/timeout with small backoff
			if attempt < 3 && (strings.Contains(stderr.String(), "504") || strings.Contains(strings.ToLower(stderr.String()), "timeout")) {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if err := json.Unmarshal(stdout.Bytes(), &prs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
		}
		return filterDependabotPRs(processPRs(prs)), nil
	}

	// Fallback: should not reach here because we return on success or error above
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("unknown error fetching PRs")
}

// fetchPRsWithDateSplit fetches PRs by splitting date range into chunks for parallel processing
func fetchPRsWithDateSplit(repo string, since, until, author, label string, includeOpen bool) ([]PullRequest, error) {
	const maxWorkers = 5
	chunkSize := 14 * 24 * time.Hour // 2-week chunks to reduce GraphQL load

	// Parse dates
	sinceTime, _ := time.Parse("2006-01-02", since)
	untilTime, _ := time.Parse("2006-01-02", until)

	// If date range is small, use single request
	if untilTime.Sub(sinceTime) < chunkSize {
		return fetchPRsSingle(repo, since, until, author, label, includeOpen)
	}

	// Split into chunks for better parallelization
	var dateRanges [][]string
	current := sinceTime
	for current.Before(untilTime) {
		end := current.Add(chunkSize)
		if end.After(untilTime) {
			end = untilTime
		}
		dateRanges = append(dateRanges, []string{
			current.Format("2006-01-02"),
			end.Format("2006-01-02"),
		})
		current = end
	}

	// Start shiba animation for parallel fetching
	spinner := animation.NewShibaSpinner(fmt.Sprintf("Fetching PRs in parallel (%d chunks, %d workers)...", len(dateRanges), maxWorkers), false)
	spinner.Start()
	defer spinner.Stop()

	// Channel for work distribution
	jobs := make(chan []string, len(dateRanges))
	results := make(chan []PullRequest, len(dateRanges))
	errors := make(chan error, len(dateRanges))

	// Worker pool
	var wg sync.WaitGroup
	for w := 0; w < maxWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for dateRange := range jobs {
				prs, err := fetchPRsSingle(repo, dateRange[0], dateRange[1], author, label, includeOpen)
				if err != nil {
					errors <- err
					return
				}
				results <- prs
				fmt.Printf("✅ Fetched %d PRs for %s to %s\n", len(prs), dateRange[0], dateRange[1])
			}
		}()
	}

	// Send jobs
	go func() {
		for _, dateRange := range dateRanges {
			jobs <- dateRange
		}
		close(jobs)
	}()

	// Wait for all workers to complete
	go func() {
		wg.Wait()
		close(results)
		close(errors)
	}()

	// Collect results
	var allPRs []PullRequest
	var lastError error

	for {
		select {
		case prs, ok := <-results:
			if !ok {
				results = nil
			} else {
				allPRs = append(allPRs, prs...)
			}
		case err, ok := <-errors:
			if !ok {
				errors = nil
			} else {
				lastError = err
			}
		}

		if results == nil && errors == nil {
			break
		}
	}

	if lastError != nil {
		return nil, lastError
	}

	deduped := deduplicatePRs(filterDependabotPRs(allPRs))
	if len(deduped) != len(allPRs) {
		fmt.Printf("ℹ️  Removed %d duplicate PRs after chunked fetch\n", len(allPRs)-len(deduped))
	}

	fmt.Printf("🎉 Total unique PRs fetched: %d\n", len(deduped))
	return deduped, nil
}

// Comment represents a PR comment
type Comment struct {
	ID        string    `json:"id"`
	Author    Author    `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
	Body      string    `json:"body"`
}

// Author represents a GitHub user
type Author struct {
	Login string `json:"login"`
}

// FetchPRCommentTiming fetches comment timing data for PRs using GraphQL
func FetchPRCommentTiming(repo string, prs []PullRequest) []PullRequest {
	// Start shiba animation for comment analysis
	spinner := animation.NewShibaSpinner(fmt.Sprintf("Analyzing review comments for %d PRs...", len(prs)), false)
	spinner.Start()
	defer spinner.Stop()

	// Split repo into owner and name
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		fmt.Printf("❌ Invalid repo format: %s\n", repo)
		return prs
	}
	owner, repoName := parts[0], parts[1]

	// Limit to first 100 PRs for performance (can be made configurable)
	limit := 100
	if len(prs) < limit {
		limit = len(prs)
	}

	// Also try some PRs from the middle and end of the list to increase chances of finding comments
	var selectedPRs []PullRequest
	if len(prs) > limit {
		// Take first 80, 10 from middle, 10 from end for better coverage
		selectedPRs = append(selectedPRs, prs[:80]...)
		middle := len(prs) / 2
		if middle+10 < len(prs) {
			selectedPRs = append(selectedPRs, prs[middle:middle+10]...)
		}
		if len(prs) >= 10 {
			selectedPRs = append(selectedPRs, prs[len(prs)-10:]...)
		}
	} else {
		selectedPRs = prs[:limit]
	}

	// Fetch review comment counts using REST API (skip general PR comments)
	// Only process PRs that are likely to have review comments (merged PRs)
	var prsToCheck []PullRequest
	for _, pr := range selectedPRs {
		if pr.Merged || pr.State == "CLOSED" {
			prsToCheck = append(prsToCheck, pr)
		}
	}

	reviewCommentCounts := fetchPRReviewCommentCounts(owner, repoName, prsToCheck)

	// Update PRs with review comment counts only
	for i := range prs {
		reviewCount := reviewCommentCounts[prs[i].Number]

		// Count approvals as “review comments” for coverage purposes
		approvalCount := 0
		for _, r := range prs[i].Reviews {
			if strings.EqualFold(r.State, "APPROVED") {
				approvalCount++
			}
		}

		prs[i].ReviewCommentCount = reviewCount + approvalCount
		prs[i].CommentCount = 0 // not tracking issue-style comments here
	}

	// Animation will be stopped by defer, then show completion message
	time.Sleep(100 * time.Millisecond) // Brief pause before completion
	fmt.Printf("✅ Comment timing analysis complete\n")
	return prs
}

// FetchReopenEvents marks PRs that were reopened and captures the first reopened timestamp.
func FetchReopenEvents(repo string, prs []PullRequest) []PullRequest {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return prs
	}
	owner, repoName := parts[0], parts[1]

	// Only check merged/closed PRs to limit API calls
	var targets []PullRequest
	for _, pr := range prs {
		if pr.State == "CLOSED" || pr.State == "MERGED" || pr.Merged {
			targets = append(targets, pr)
		}
	}

	if len(targets) == 0 {
		return prs
	}

	fmt.Printf("🔍 Checking reopen events for %d PRs...\n", len(targets))

	type result struct {
		number int
		time   time.Time
	}

	jobs := make(chan PullRequest, len(targets))
	results := make(chan result, len(targets))
	const workers = 4

	for w := 0; w < workers; w++ {
		go func() {
			for pr := range jobs {
				reopenedAt := fetchFirstReopenEvent(owner, repoName, pr.Number)
				if !reopenedAt.IsZero() {
					results <- result{number: pr.Number, time: reopenedAt}
				} else {
					results <- result{number: pr.Number}
				}
			}
		}()
	}

	for _, pr := range targets {
		jobs <- pr
	}
	close(jobs)

	reopenTimes := make(map[int]time.Time, len(targets))
	for i := 0; i < len(targets); i++ {
		r := <-results
		if !r.time.IsZero() {
			reopenTimes[r.number] = r.time
		}
	}

	for i := range prs {
		if t, ok := reopenTimes[prs[i].Number]; ok {
			prs[i].IsReopened = true
			prs[i].FirstReopenedAt = t
		}
	}

	return prs
}

// fetchFirstReopenEvent fetches the first "reopened" event for a PR using the issues events API.
func fetchFirstReopenEvent(owner, repo string, number int) time.Time {
	cmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/%s/issues/%d/events", owner, repo, number),
		"--json", "event,created_at")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return time.Time{}
	}

	var events []struct {
		Event     string    `json:"event"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &events); err != nil {
		return time.Time{}
	}

	earliest := time.Time{}
	for _, ev := range events {
		if strings.EqualFold(ev.Event, "reopened") {
			if earliest.IsZero() || ev.CreatedAt.Before(earliest) {
				earliest = ev.CreatedAt
			}
		}
	}
	return earliest
}

// PRCommentTiming holds timing calculations for a single PR
type PRCommentTiming struct {
	FirstCommentTime      time.Time
	FirstReviewTime       time.Time
	TimeToFirstComment    time.Duration
	TimeToFirstReview     time.Duration
	AvgReviewResponseTime time.Duration
	CommentCount          int
}

// fetchSinglePRCommentTiming fetches comment timing for a single PR
func fetchSinglePRCommentTiming(repo string, prNumber int) PRCommentTiming {
	timing := PRCommentTiming{}

	// Fetch PR comments
	args := []string{
		"pr", "view", fmt.Sprintf("%d", prNumber),
		"--repo", repo,
		"--json", "comments,reviews,createdAt",
	}

	cmd := exec.Command("gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Silently ignore errors for individual PRs
		return timing
	}

	var prData struct {
		CreatedAt time.Time `json:"createdAt"`
		Comments  []Comment `json:"comments"`
		Reviews   []struct {
			Author      Author    `json:"author"`
			SubmittedAt time.Time `json:"submittedAt"`
		} `json:"reviews"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &prData); err != nil {
		return timing
	}

	// Calculate comment count and first comment time
	timing.CommentCount = len(prData.Comments)
	if len(prData.Comments) > 0 {
		timing.FirstCommentTime = prData.Comments[0].CreatedAt
		timing.TimeToFirstComment = timing.FirstCommentTime.Sub(prData.CreatedAt)
	}

	// Calculate first review time
	if len(prData.Reviews) > 0 {
		timing.FirstReviewTime = prData.Reviews[0].SubmittedAt
		timing.TimeToFirstReview = timing.FirstReviewTime.Sub(prData.CreatedAt)
	}

	// Calculate average review response time (simplified)
	// This is a basic implementation - could be enhanced with more sophisticated logic
	if len(prData.Reviews) > 1 {
		var totalResponseTime time.Duration
		var responseCount int

		for i := 1; i < len(prData.Reviews); i++ {
			responseTime := prData.Reviews[i].SubmittedAt.Sub(prData.Reviews[i-1].SubmittedAt)
			if responseTime > 0 && responseTime < 7*24*time.Hour { // Filter out unrealistic times
				totalResponseTime += responseTime
				responseCount++
			}
		}

		if responseCount > 0 {
			timing.AvgReviewResponseTime = totalResponseTime / time.Duration(responseCount)
		}
	}

	return timing
}

// fetchPRCommentCountsGraphQL fetches comment counts using GitHub GraphQL API
func fetchPRCommentCountsGraphQL(owner, repo string, prs []PullRequest) map[int]int {
	commentCounts := make(map[int]int)

	// Build PR numbers for query
	prNumbers := make([]int, len(prs))
	for i, pr := range prs {
		prNumbers[i] = pr.Number
	}

	// Create GraphQL query for multiple PRs
	query := buildPRCommentQuery(owner, repo, prNumbers)

	// Execute GraphQL query using gh api
	cmd := exec.Command("gh", "api", "graphql", "-f", fmt.Sprintf("query=%s", query))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("❌ GraphQL query failed: %s\n", stderr.String())
		return commentCounts
	}

	// Parse GraphQL response
	var response struct {
		Data struct {
			Repository map[string]struct {
				Number   int `json:"number"`
				Comments struct {
					TotalCount int `json:"totalCount"`
				} `json:"comments"`
			} `json:"repository"`
		} `json:"data"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		fmt.Printf("❌ Failed to parse GraphQL response: %v\n", err)
		return commentCounts
	}

	// Extract comment counts
	for _, pr := range response.Data.Repository {
		commentCounts[pr.Number] = pr.Comments.TotalCount
	}

	return commentCounts
}

// buildPRCommentQuery builds a GraphQL query for fetching PR comment counts
func buildPRCommentQuery(owner, repo string, prNumbers []int) string {
	// Build individual PR queries
	var prQueries []string
	for i, prNumber := range prNumbers {
		if i >= 30 { // Limit to prevent query complexity issues
			break
		}
		prQueries = append(prQueries, fmt.Sprintf(`
		pr%d: pullRequest(number: %d) {
			number
			comments {
				totalCount
			}
		}`, i, prNumber))
	}

	query := fmt.Sprintf(`{
		repository(owner: "%s", name: "%s") {
			%s
		}
	}`, owner, repo, strings.Join(prQueries, "\n"))

	return query
}

// fetchMergedPRsByRange fetches merged PRs by mergedAt date range to catch PRs created before the window.
func fetchMergedPRsByRange(repo, since, until, author, label string) ([]PullRequest, error) {
	args := buildMergedPRArgs(repo, since, until, author, label)

	cmd := exec.Command("gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh command failed: %s\n%s", err, stderr.String())
	}

	var prs []PullRequest
	if err := json.Unmarshal(stdout.Bytes(), &prs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	return processPRs(prs), nil
}

func buildMergedPRArgs(repo, since, until, author, label string) []string {
	args := []string{
		"pr", "list",
		"--repo", repo,
		"--state", "merged",
		"--json", "number,title,createdAt,mergedAt,closedAt,author,additions,deletions,changedFiles,isDraft,state,mergedBy,reviews,baseRefName,headRefName",
		"--search", fmt.Sprintf("merged:%s..%s", since, until),
		"--limit", "1000",
	}
	if author != "" {
		args = append(args, "--author", author)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	return args
}

// fetchPRReviewCommentCounts fetches review comment counts (excluding replies) using REST API with parallel processing
func fetchPRReviewCommentCounts(owner, repo string, prs []PullRequest) map[int]int {
	reviewCommentCounts := make(map[int]int)

	// Use worker pool for parallel processing
	maxWorkers := 5 // Reasonable limit to avoid hitting GitHub API rate limits
	jobs := make(chan PullRequest, len(prs))
	results := make(chan struct {
		prNumber int
		count    int
	}, len(prs))

	// Start workers
	for w := 0; w < maxWorkers; w++ {
		go func() {
			for pr := range jobs {
				count := fetchSinglePRReviewCommentCount(owner, repo, pr.Number)
				results <- struct {
					prNumber int
					count    int
				}{pr.Number, count}
			}
		}()
	}

	// Send jobs
	for _, pr := range prs {
		jobs <- pr
	}
	close(jobs)

	// Collect results
	for i := 0; i < len(prs); i++ {
		result := <-results
		reviewCommentCounts[result.prNumber] = result.count
	}

	return reviewCommentCounts
}

// fetchSinglePRReviewCommentCount fetches review comment count for a single PR (excluding replies)
func fetchSinglePRReviewCommentCount(owner, repo string, prNumber int) int {
	// Use REST API to get review comments with in_reply_to_id field
	cmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/%s/pulls/%d/comments", owner, repo, prNumber))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Add timeout to avoid hanging on slow API calls
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case err := <-done:
		if err != nil {
			// Silently ignore errors for individual PRs
			return 0
		}
	case <-time.After(10 * time.Second):
		// Timeout after 10 seconds
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return 0
	}

	var comments []struct {
		ID          int    `json:"id"`
		InReplyToID *int   `json:"in_reply_to_id"`
		Body        string `json:"body"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &comments); err != nil {
		return 0
	}

	// Count only original comments (not replies)
	originalComments := 0
	for _, comment := range comments {
		if comment.InReplyToID == nil {
			originalComments++
		}
	}

	return originalComments
}

// buildBaseArgs builds the base arguments for gh pr list command
func buildBaseArgs(repo string, since, until, author, label string, includeOpen bool) []string {
	args := []string{
		"pr", "list",
		"--repo", repo,
		"--json", "number,title,createdAt,mergedAt,closedAt,author,additions,deletions,changedFiles,isDraft,state,mergedBy,reviews,baseRefName,headRefName",
	}

	// Add state filter
	if !includeOpen {
		args = append(args, "--state", "closed")
	} else {
		args = append(args, "--state", "all")
	}

	// Add author filter
	if author != "" {
		args = append(args, "--author", author)
	}

	// Add label filter
	if label != "" {
		args = append(args, "--label", label)
	}

	// Add created date filter using search query
	var searchQueries []string
	if since != "" && until != "" {
		searchQueries = append(searchQueries, fmt.Sprintf("created:%s..%s", since, until))
	} else if since != "" {
		searchQueries = append(searchQueries, fmt.Sprintf("created:>=%s", since))
	} else if until != "" {
		searchQueries = append(searchQueries, fmt.Sprintf("created:<=%s", until))
	}

	if len(searchQueries) > 0 {
		searchQuery := strings.Join(searchQueries, " ")
		args = append(args, "--search", searchQuery)
	}

	return args
}

// processPRs processes PRs to calculate lead time and set merged flag
func processPRs(prs []PullRequest) []PullRequest {
	for i := range prs {
		// Set Merged flag based on state
		prs[i].Merged = (prs[i].State == "MERGED")

		if prs[i].Merged && !prs[i].MergedAt.IsZero() {
			prs[i].LeadTime = prs[i].MergedAt.Sub(prs[i].CreatedAt)
		} else if !prs[i].ClosedAt.IsZero() {
			prs[i].LeadTime = prs[i].ClosedAt.Sub(prs[i].CreatedAt)
		}
	}
	return prs
}

// filterDependabotPRs drops Dependabot PRs to avoid skewing release/PR metrics.
func filterDependabotPRs(prs []PullRequest) []PullRequest {
	filtered := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		if isDependabotPR(pr) {
			continue
		}
		filtered = append(filtered, pr)
	}
	return filtered
}

func isDependabotPR(pr PullRequest) bool {
	login := strings.ToLower(strings.TrimSpace(pr.Author.Login))
	head := strings.ToLower(strings.TrimSpace(pr.HeadRefName))

	// The author is normally "dependabot[bot]". Keep compatibility with
	// Dependabot Preview and GitHub App login representations as well.
	return strings.Contains(login, "dependabot") ||
		strings.HasPrefix(head, "dependabot/")
}

// deduplicatePRs removes duplicate pull requests by PR number, keeping the most recent occurrence.
func deduplicatePRs(prs []PullRequest) []PullRequest {
	seen := make(map[int]PullRequest)

	for _, pr := range prs {
		existing, exists := seen[pr.Number]
		if !exists || pr.CreatedAt.After(existing.CreatedAt) {
			seen[pr.Number] = pr
		}
	}

	deduped := make([]PullRequest, 0, len(seen))
	for _, pr := range seen {
		deduped = append(deduped, pr)
	}

	sort.Slice(deduped, func(i, j int) bool {
		if deduped[i].CreatedAt.Equal(deduped[j].CreatedAt) {
			return deduped[i].Number < deduped[j].Number
		}
		return deduped[i].CreatedAt.Before(deduped[j].CreatedAt)
	})

	return deduped
}
