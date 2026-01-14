package stats

import (
	"sort"
	"strings"
	"time"
	"visuche/internal/github"
)

// Stats holds the calculated statistics.
type Stats struct {
	AverageLeadTime             time.Duration
	MedianLeadTime              time.Duration
	MergedPRs                   int
	TotalPRs                    int
	AverageFilesChanged         float64
	AverageAdditions            float64
	AverageDeletions            float64
	AverageReviewTime           time.Duration
	MedianReviewTime            time.Duration
	AverageMergeWaitTime        time.Duration
	MedianMergeWaitTime         time.Duration
	AverageCommitToPRTime       time.Duration
	AverageCommitsPerPR         float64
	ForcePushRate               float64 // This might be hard to calculate accurately from current data
	WIPPRCount                  int
	AverageReviewersPerPR       float64
	SelfMergeRate               float64
	SelfMergeWithApproval       int
	SelfMergeWithoutApproval    int
	MergeTypeTrend              map[string]float64 // squash, merge, rebase
	CommitFrequencyPerWeek      float64
	ReleaseCount                int
	AverageApprovalToMerge      time.Duration
	MedianApprovalToMerge       time.Duration
	ReopenedPRs                 int
	ReopenRate                  float64
	AverageReopenToMerge        time.Duration
	MedianReopenToMerge         time.Duration
	RevertLikeMerges            int
	HotfixMerges                int
	AverageHotfixAfterRelease   time.Duration
	MedianHotfixAfterRelease    time.Duration
	HotfixWithoutReleaseContext int
	FeatureToReleaseCount       int
	AverageFeatureToRelease     time.Duration
	MedianFeatureToRelease      time.Duration
	ReleaseToMainCount          int
	AverageReleaseToMain        time.Duration
	MedianReleaseToMain         time.Duration

	// Comment timing metrics
	AverageTimeToFirstComment time.Duration
	MedianTimeToFirstComment  time.Duration
	AverageTimeToFirstReview  time.Duration
	MedianTimeToFirstReview   time.Duration
	AverageReviewResponseTime time.Duration
	PRsWithComments           int
	PRsWithReviews            int

	// Comment quantity metrics
	AverageCommentsPerPR float64
	MedianCommentsPerPR  float64
	CommentDensity       float64 // Comments per 100 lines of code changed
	MaxCommentsInPR      int
	PRsWithoutComments   int

	// Review comment metrics (code review comments, excluding replies)
	AverageReviewCommentsPerPR float64
	MedianReviewCommentsPerPR  float64
	MaxReviewCommentsInPR      int
	PRsWithReviewComments      int
	PRsWithoutReviewComments   int
}

func CalculateStats(prs []github.PullRequest) Stats {
	var totalLeadTime time.Duration
	var mergedCount int
	var leadTimes []time.Duration

	var totalFilesChanged int
	var totalAdditions int
	var totalDeletions int
	var totalReviewTime time.Duration
	var totalMergeWaitTime time.Duration
	var totalApprovalToMerge time.Duration
	var reviewPRCount int
	var reviewDurations []time.Duration
	var mergeWaitDurations []time.Duration
	var approvalToMergeDurations []time.Duration
	var reopenToMergeDurations []time.Duration
	var totalCommitToPRTime time.Duration
	var totalCommits int
	var validCommitToPRCount int
	var totalReviewers int
	var selfMergedCount int
	var selfMergedWithApproval int
	var selfMergedWithoutApproval int
	var approvalMergeCount int
	mergeTypeCounts := make(map[string]int)
	var reopenedPRs int
	var revertLikeMerges int
	var releaseMergeTimes []time.Time
	var hotfixMerges int
	var hotfixDurations []time.Duration
	var hotfixWithoutRelease int
	type hotfixRecord struct {
		mergedAt time.Time
	}
	var hotfixRecords []hotfixRecord
	var featureToReleaseDurations []time.Duration
	var releaseToMainDurations []time.Duration
	var featureToReleaseCount int
	var releaseToMainCount int

	var openPRs int
	var earliestPRDate, latestPRDate time.Time

	// Comment timing variables
	var totalTimeToFirstComment, totalTimeToFirstReview, totalReviewResponseTime time.Duration
	var timeToFirstCommentSlice, timeToFirstReviewSlice []time.Duration
	var prsWithComments, prsWithReviews, prsWithResponseTime int

	// Comment quantity variables
	var totalComments int
	var commentCountSlice []int
	var maxComments int
	var prsWithoutComments int
	var releaseCount int

	// Review comment quantity variables
	var totalReviewComments int
	var reviewCommentCountSlice []int
	var maxReviewComments int
	var prsWithReviewComments int
	var prsWithoutReviewComments int

	for _, pr := range prs {
		headLower := strings.ToLower(pr.HeadRefName)
		baseLower := strings.ToLower(pr.BaseRefName)

		// Track date range for commit frequency calculation
		if earliestPRDate.IsZero() || pr.CreatedAt.Before(earliestPRDate) {
			earliestPRDate = pr.CreatedAt
		}
		if latestPRDate.IsZero() || pr.CreatedAt.After(latestPRDate) {
			latestPRDate = pr.CreatedAt
		}
		// Lead Time
		if pr.Merged {
			totalLeadTime += pr.LeadTime
			mergedCount++
			leadTimes = append(leadTimes, pr.LeadTime)
		}

		// Average Files Changed, Additions, Deletions
		totalFilesChanged += pr.ChangedFiles
		totalAdditions += pr.Additions
		totalDeletions += pr.Deletions

		// Sort reviews by time once for multiple metrics
		var firstReviewTime time.Time
		var lastReviewTime time.Time
		if len(pr.Reviews) > 0 {
			sort.Slice(pr.Reviews, func(i, j int) bool {
				return pr.Reviews[i].SubmittedAt.Before(pr.Reviews[j].SubmittedAt)
			})
			firstReviewTime = pr.Reviews[0].SubmittedAt
			lastReviewTime = pr.Reviews[len(pr.Reviews)-1].SubmittedAt
		}

		// Average Review Time (creation -> first review)
		if !firstReviewTime.IsZero() {
			reviewTime := firstReviewTime.Sub(pr.CreatedAt)
			if reviewTime > 0 {
				totalReviewTime += reviewTime
				reviewPRCount++
				reviewDurations = append(reviewDurations, reviewTime)
			}
		}

		// Average Merge Wait Time (Approximation: last review to merge time)
		if pr.Merged && !lastReviewTime.IsZero() {
			start := lastReviewTime

			// For main/master targets, do not count draft time as "waiting to merge" (unless hotfix prefix).
			if (strings.EqualFold(pr.BaseRefName, "main") || strings.EqualFold(pr.BaseRefName, "master")) &&
				pr.IsDraft &&
				!strings.HasPrefix(strings.ToLower(pr.HeadRefName), "hotfix") {
				readyTime := firstReviewTime
				if readyTime.IsZero() {
					readyTime = pr.MergedAt
				}
				if start.Before(readyTime) {
					start = readyTime
				}
			}

			if pr.MergedAt.After(start) {
				mergeWaitTime := pr.MergedAt.Sub(start)
				totalMergeWaitTime += mergeWaitTime
				mergeWaitDurations = append(mergeWaitDurations, mergeWaitTime)
			}
		}

		// Approval -> merge time
		if pr.Merged {
			var lastApproval time.Time
			for _, r := range pr.Reviews {
				if strings.EqualFold(r.State, "APPROVED") && r.SubmittedAt.After(lastApproval) {
					lastApproval = r.SubmittedAt
				}
			}
			if !lastApproval.IsZero() && pr.MergedAt.After(lastApproval) {
				totalApprovalToMerge += pr.MergedAt.Sub(lastApproval)
				approvalMergeCount++
				approvalToMergeDurations = append(approvalToMergeDurations, pr.MergedAt.Sub(lastApproval))
			}
		}

		// Average Commits per PR - disabled due to GraphQL complexity
		// totalCommits += len(pr.Commits)

		// Commit to PR creation time - disabled due to GraphQL complexity
		// if len(pr.Commits) > 0 {
		// 	// Find the earliest commit
		// 	var earliestCommit time.Time
		// 	for _, commit := range pr.Commits {
		// 		if earliestCommit.IsZero() || commit.CommittedDate.Before(earliestCommit) {
		// 			earliestCommit = commit.CommittedDate
		// 		}
		// 	}
		// 	if !earliestCommit.IsZero() {
		// 		commitToPRTime := pr.CreatedAt.Sub(earliestCommit)
		// 		if commitToPRTime >= 0 { // Only count positive durations
		// 			totalCommitToPRTime += commitToPRTime
		// 			validCommitToPRCount++
		// 		}
		// 	}
		// }

		// WIP PR Count
		if pr.State == "OPEN" && pr.IsDraft {
			openPRs++
		}

		// Average Reviewers per PR
		reviewers := make(map[string]bool)
		for _, review := range pr.Reviews {
			reviewers[review.Author.Login] = true
		}
		totalReviewers += len(reviewers)

		// Self-Merge Rate
		if pr.Merged && pr.Author.Login == pr.MergedBy.Login {
			selfMergedCount++
			hasApproval := false
			for _, r := range pr.Reviews {
				if strings.EqualFold(r.State, "APPROVED") {
					hasApproval = true
					break
				}
			}
			if hasApproval {
				selfMergedWithApproval++
			} else {
				selfMergedWithoutApproval++
			}
		}

		// Reopened metrics
		if pr.IsReopened {
			reopenedPRs++
			if pr.Merged && !pr.FirstReopenedAt.IsZero() && pr.MergedAt.After(pr.FirstReopenedAt) {
				duration := pr.MergedAt.Sub(pr.FirstReopenedAt)
				reopenToMergeDurations = append(reopenToMergeDurations, duration)
			}
		}

		// Merge Type Trend (Approximation based on merge commit presence and PR state)
		if pr.Merged {
			if pr.MergeCommit.Oid != "" {
				// This is a heuristic. GitHub API doesn't directly expose merge method.
				// If a merge commit exists, it's likely a merge or squash.
				// Further analysis of commit history would be needed for true accuracy.
				mergeTypeCounts["merge/squash"]++
			} else {
				// Could be rebase and merge, or other scenarios
				mergeTypeCounts["rebase/other"]++
			}

			// Revert-like detection (title heuristic)
			titleLower := strings.ToLower(pr.Title)
			if strings.Contains(titleLower, "revert") {
				revertLikeMerges++
			}

			// Hotfix detection (head branch prefix)
			if strings.HasPrefix(strings.ToLower(pr.HeadRefName), "hotfix") {
				hotfixMerges++
				if !pr.MergedAt.IsZero() {
					hotfixRecords = append(hotfixRecords, hotfixRecord{mergedAt: pr.MergedAt})
				}
			}

			// Feature/Story -> release branch lead time
			if strings.HasPrefix(headLower, "feature/") || strings.HasPrefix(headLower, "story/") {
				if strings.HasPrefix(baseLower, "release/") && !pr.MergedAt.IsZero() {
					featureToReleaseCount++
					featureToReleaseDurations = append(featureToReleaseDurations, pr.MergedAt.Sub(pr.CreatedAt))
				}
			}

			// Release branch -> main/master lead time
			if strings.HasPrefix(headLower, "release/") && (baseLower == "main" || baseLower == "master") && !pr.MergedAt.IsZero() {
				releaseToMainCount++
				releaseToMainDurations = append(releaseToMainDurations, pr.MergedAt.Sub(pr.CreatedAt))
				releaseCount++
				releaseMergeTimes = append(releaseMergeTimes, pr.MergedAt)
			}
		}

		// Comment timing statistics
		if pr.TimeToFirstComment > 0 {
			totalTimeToFirstComment += pr.TimeToFirstComment
			timeToFirstCommentSlice = append(timeToFirstCommentSlice, pr.TimeToFirstComment)
		}

		if pr.TimeToFirstReview > 0 {
			totalTimeToFirstReview += pr.TimeToFirstReview
			timeToFirstReviewSlice = append(timeToFirstReviewSlice, pr.TimeToFirstReview)
			prsWithReviews++
		}

		if pr.AvgReviewResponseTime > 0 {
			totalReviewResponseTime += pr.AvgReviewResponseTime
			prsWithResponseTime++
		}

		// Comment quantity statistics
		totalComments += pr.CommentCount
		commentCountSlice = append(commentCountSlice, pr.CommentCount)
		if pr.CommentCount > maxComments {
			maxComments = pr.CommentCount
		}
		if pr.CommentCount > 0 {
			prsWithComments++
		} else {
			prsWithoutComments++
		}

		// Review comment quantity statistics
		totalReviewComments += pr.ReviewCommentCount
		reviewCommentCountSlice = append(reviewCommentCountSlice, pr.ReviewCommentCount)
		if pr.ReviewCommentCount > maxReviewComments {
			maxReviewComments = pr.ReviewCommentCount
		}
		if pr.ReviewCommentCount > 0 {
			prsWithReviewComments++
		} else {
			prsWithoutReviewComments++
		}
	}

	var avgLeadTime time.Duration
	if mergedCount > 0 {
		avgLeadTime = totalLeadTime / time.Duration(mergedCount)
	}

	var medianLeadTime time.Duration
	if len(leadTimes) > 0 {
		sort.Slice(leadTimes, func(i, j int) bool {
			return leadTimes[i] < leadTimes[j]
		})

		mid := len(leadTimes) / 2
		if len(leadTimes)%2 == 0 {
			medianLeadTime = (leadTimes[mid-1] + leadTimes[mid]) / 2
		} else {
			medianLeadTime = leadTimes[mid]
		}
	}

	numPRs := float64(len(prs))

	avgFilesChanged := 0.0
	avgAdditions := 0.0
	avgDeletions := 0.0
	if numPRs > 0 {
		avgFilesChanged = float64(totalFilesChanged) / numPRs
		avgAdditions = float64(totalAdditions) / numPRs
		avgDeletions = float64(totalDeletions) / numPRs
	}

	avgReviewTime := time.Duration(0)
	if reviewPRCount > 0 { // Average only across PRs that actually have review data and valid timestamps
		avgReviewTime = totalReviewTime / time.Duration(reviewPRCount)
	}
	medianReviewTime := time.Duration(0)
	if len(reviewDurations) > 0 {
		sort.Slice(reviewDurations, func(i, j int) bool { return reviewDurations[i] < reviewDurations[j] })
		mid := len(reviewDurations) / 2
		if len(reviewDurations)%2 == 0 {
			medianReviewTime = (reviewDurations[mid-1] + reviewDurations[mid]) / 2
		} else {
			medianReviewTime = reviewDurations[mid]
		}
	}

	avgMergeWaitTime := time.Duration(0)
	if mergedCount > 0 {
		avgMergeWaitTime = totalMergeWaitTime / time.Duration(mergedCount)
	}
	medianMergeWaitTime := time.Duration(0)
	if len(mergeWaitDurations) > 0 {
		sort.Slice(mergeWaitDurations, func(i, j int) bool { return mergeWaitDurations[i] < mergeWaitDurations[j] })
		mid := len(mergeWaitDurations) / 2
		if len(mergeWaitDurations)%2 == 0 {
			medianMergeWaitTime = (mergeWaitDurations[mid-1] + mergeWaitDurations[mid]) / 2
		} else {
			medianMergeWaitTime = mergeWaitDurations[mid]
		}
	}

	avgApprovalToMerge := time.Duration(0)
	if approvalMergeCount > 0 {
		avgApprovalToMerge = totalApprovalToMerge / time.Duration(approvalMergeCount)
	}
	medianApprovalToMerge := time.Duration(0)
	if len(approvalToMergeDurations) > 0 {
		sort.Slice(approvalToMergeDurations, func(i, j int) bool { return approvalToMergeDurations[i] < approvalToMergeDurations[j] })
		mid := len(approvalToMergeDurations) / 2
		if len(approvalToMergeDurations)%2 == 0 {
			medianApprovalToMerge = (approvalToMergeDurations[mid-1] + approvalToMergeDurations[mid]) / 2
		} else {
			medianApprovalToMerge = approvalToMergeDurations[mid]
		}
	}

	avgReopenToMerge := time.Duration(0)
	medianReopenToMerge := time.Duration(0)
	if len(reopenToMergeDurations) > 0 {
		var total time.Duration
		for _, d := range reopenToMergeDurations {
			total += d
		}
		avgReopenToMerge = total / time.Duration(len(reopenToMergeDurations))

		sort.Slice(reopenToMergeDurations, func(i, j int) bool { return reopenToMergeDurations[i] < reopenToMergeDurations[j] })
		mid := len(reopenToMergeDurations) / 2
		if len(reopenToMergeDurations)%2 == 0 {
			medianReopenToMerge = (reopenToMergeDurations[mid-1] + reopenToMergeDurations[mid]) / 2
		} else {
			medianReopenToMerge = reopenToMergeDurations[mid]
		}
	}

	// Hotfix after release durations
	if len(releaseMergeTimes) > 0 {
		sort.Slice(releaseMergeTimes, func(i, j int) bool { return releaseMergeTimes[i].Before(releaseMergeTimes[j]) })
	}
	if len(hotfixRecords) > 0 {
		for _, h := range hotfixRecords {
			idx := sort.Search(len(releaseMergeTimes), func(i int) bool {
				return releaseMergeTimes[i].After(h.mergedAt) || releaseMergeTimes[i].Equal(h.mergedAt)
			})
			if idx == 0 {
				hotfixWithoutRelease++
				continue
			}
			prevRelease := releaseMergeTimes[idx-1]
			if prevRelease.Before(h.mergedAt) {
				hotfixDurations = append(hotfixDurations, h.mergedAt.Sub(prevRelease))
			}
		}
	}

	avgHotfixAfterRelease := time.Duration(0)
	medianHotfixAfterRelease := time.Duration(0)
	if len(hotfixDurations) > 0 {
		var total time.Duration
		for _, d := range hotfixDurations {
			total += d
		}
		avgHotfixAfterRelease = total / time.Duration(len(hotfixDurations))

		sort.Slice(hotfixDurations, func(i, j int) bool { return hotfixDurations[i] < hotfixDurations[j] })
		mid := len(hotfixDurations) / 2
		if len(hotfixDurations)%2 == 0 {
			medianHotfixAfterRelease = (hotfixDurations[mid-1] + hotfixDurations[mid]) / 2
		} else {
			medianHotfixAfterRelease = hotfixDurations[mid]
		}
	}

	// Feature -> release durations
	avgFeatureToRelease := time.Duration(0)
	medianFeatureToRelease := time.Duration(0)
	if len(featureToReleaseDurations) > 0 {
		var total time.Duration
		for _, d := range featureToReleaseDurations {
			total += d
		}
		avgFeatureToRelease = total / time.Duration(len(featureToReleaseDurations))

		sort.Slice(featureToReleaseDurations, func(i, j int) bool { return featureToReleaseDurations[i] < featureToReleaseDurations[j] })
		mid := len(featureToReleaseDurations) / 2
		if len(featureToReleaseDurations)%2 == 0 {
			medianFeatureToRelease = (featureToReleaseDurations[mid-1] + featureToReleaseDurations[mid]) / 2
		} else {
			medianFeatureToRelease = featureToReleaseDurations[mid]
		}
	}

	// Release -> main durations
	avgReleaseToMain := time.Duration(0)
	medianReleaseToMain := time.Duration(0)
	if len(releaseToMainDurations) > 0 {
		var total time.Duration
		for _, d := range releaseToMainDurations {
			total += d
		}
		avgReleaseToMain = total / time.Duration(len(releaseToMainDurations))

		sort.Slice(releaseToMainDurations, func(i, j int) bool { return releaseToMainDurations[i] < releaseToMainDurations[j] })
		mid := len(releaseToMainDurations) / 2
		if len(releaseToMainDurations)%2 == 0 {
			medianReleaseToMain = (releaseToMainDurations[mid-1] + releaseToMainDurations[mid]) / 2
		} else {
			medianReleaseToMain = releaseToMainDurations[mid]
		}
	}

	avgCommitsPerPR := 0.0
	if numPRs > 0 {
		avgCommitsPerPR = float64(totalCommits) / numPRs
	}

	avgCommitToPRTime := time.Duration(0)
	if validCommitToPRCount > 0 {
		avgCommitToPRTime = totalCommitToPRTime / time.Duration(validCommitToPRCount)
	}

	avgReviewersPerPR := 0.0
	if numPRs > 0 {
		avgReviewersPerPR = float64(totalReviewers) / numPRs
	}

	selfMergeRate := 0.0
	if mergedCount > 0 {
		selfMergeRate = float64(selfMergedCount) / float64(mergedCount) * 100.0
	}

	mergeTypeTrend := make(map[string]float64)
	if mergedCount > 0 {
		for k, v := range mergeTypeCounts {
			mergeTypeTrend[k] = float64(v) / float64(mergedCount) * 100.0
		}
	}

	reopenRate := 0.0
	if len(prs) > 0 {
		reopenRate = float64(reopenedPRs) / float64(len(prs)) * 100.0
	}

	// Calculate commit frequency per week (approximated by PR frequency since commit data is complex to fetch)
	commitFrequencyPerWeek := 0.0
	if !earliestPRDate.IsZero() && !latestPRDate.IsZero() {
		duration := latestPRDate.Sub(earliestPRDate)
		weeks := duration.Hours() / (24 * 7) // Convert to weeks
		if weeks > 0 {
			// Use PR frequency as a proxy for commit frequency
			// Multiply by average estimated commits per PR (typical range: 3-5)
			avgCommitsPerPREstimate := 3.5
			commitFrequencyPerWeek = (float64(len(prs)) / weeks) * avgCommitsPerPREstimate
		}
	}

	// Calculate comment timing statistics
	avgTimeToFirstComment := time.Duration(0)
	if prsWithComments > 0 {
		avgTimeToFirstComment = totalTimeToFirstComment / time.Duration(prsWithComments)
	}

	avgTimeToFirstReview := time.Duration(0)
	if prsWithReviews > 0 {
		avgTimeToFirstReview = totalTimeToFirstReview / time.Duration(prsWithReviews)
	}

	avgReviewResponseTime := time.Duration(0)
	if prsWithResponseTime > 0 {
		avgReviewResponseTime = totalReviewResponseTime / time.Duration(prsWithResponseTime)
	}

	// Calculate median times
	var medianTimeToFirstComment, medianTimeToFirstReview time.Duration

	if len(timeToFirstCommentSlice) > 0 {
		sort.Slice(timeToFirstCommentSlice, func(i, j int) bool {
			return timeToFirstCommentSlice[i] < timeToFirstCommentSlice[j]
		})
		mid := len(timeToFirstCommentSlice) / 2
		if len(timeToFirstCommentSlice)%2 == 0 {
			medianTimeToFirstComment = (timeToFirstCommentSlice[mid-1] + timeToFirstCommentSlice[mid]) / 2
		} else {
			medianTimeToFirstComment = timeToFirstCommentSlice[mid]
		}
	}

	if len(timeToFirstReviewSlice) > 0 {
		sort.Slice(timeToFirstReviewSlice, func(i, j int) bool {
			return timeToFirstReviewSlice[i] < timeToFirstReviewSlice[j]
		})
		mid := len(timeToFirstReviewSlice) / 2
		if len(timeToFirstReviewSlice)%2 == 0 {
			medianTimeToFirstReview = (timeToFirstReviewSlice[mid-1] + timeToFirstReviewSlice[mid]) / 2
		} else {
			medianTimeToFirstReview = timeToFirstReviewSlice[mid]
		}
	}

	// Calculate comment quantity statistics
	avgCommentsPerPR := 0.0
	if numPRs > 0 {
		avgCommentsPerPR = float64(totalComments) / numPRs
	}

	var medianCommentsPerPR float64
	if len(commentCountSlice) > 0 {
		sort.Ints(commentCountSlice)
		mid := len(commentCountSlice) / 2
		if len(commentCountSlice)%2 == 0 {
			medianCommentsPerPR = float64(commentCountSlice[mid-1]+commentCountSlice[mid]) / 2.0
		} else {
			medianCommentsPerPR = float64(commentCountSlice[mid])
		}
	}

	// Calculate comment density (comments per 100 lines of code changed)
	commentDensity := 0.0
	if totalAdditions+totalDeletions > 0 {
		commentDensity = float64(totalComments) / float64(totalAdditions+totalDeletions) * 100.0
	}

	// Calculate review comment statistics
	avgReviewCommentsPerPR := 0.0
	if numPRs > 0 {
		avgReviewCommentsPerPR = float64(totalReviewComments) / numPRs
	}

	var medianReviewCommentsPerPR float64
	if len(reviewCommentCountSlice) > 0 {
		sort.Ints(reviewCommentCountSlice)
		mid := len(reviewCommentCountSlice) / 2
		if len(reviewCommentCountSlice)%2 == 0 {
			medianReviewCommentsPerPR = float64(reviewCommentCountSlice[mid-1]+reviewCommentCountSlice[mid]) / 2.0
		} else {
			medianReviewCommentsPerPR = float64(reviewCommentCountSlice[mid])
		}
	}

	return Stats{
		AverageLeadTime:             avgLeadTime,
		MedianLeadTime:              medianLeadTime,
		MergedPRs:                   mergedCount,
		TotalPRs:                    len(prs),
		AverageFilesChanged:         avgFilesChanged,
		AverageAdditions:            avgAdditions,
		AverageDeletions:            avgDeletions,
		AverageReviewTime:           avgReviewTime,
		MedianReviewTime:            medianReviewTime,
		AverageMergeWaitTime:        avgMergeWaitTime,
		MedianMergeWaitTime:         medianMergeWaitTime,
		AverageApprovalToMerge:      avgApprovalToMerge,
		MedianApprovalToMerge:       medianApprovalToMerge,
		AverageReopenToMerge:        avgReopenToMerge,
		MedianReopenToMerge:         medianReopenToMerge,
		HotfixMerges:                hotfixMerges,
		AverageHotfixAfterRelease:   avgHotfixAfterRelease,
		MedianHotfixAfterRelease:    medianHotfixAfterRelease,
		HotfixWithoutReleaseContext: hotfixWithoutRelease,
		FeatureToReleaseCount:       featureToReleaseCount,
		AverageFeatureToRelease:     avgFeatureToRelease,
		MedianFeatureToRelease:      medianFeatureToRelease,
		ReleaseToMainCount:          releaseToMainCount,
		AverageReleaseToMain:        avgReleaseToMain,
		MedianReleaseToMain:         medianReleaseToMain,
		AverageCommitToPRTime:       avgCommitToPRTime,
		AverageCommitsPerPR:         avgCommitsPerPR,
		ForcePushRate:               0.0, // Cannot accurately calculate with current data
		WIPPRCount:                  openPRs,
		AverageReviewersPerPR:       avgReviewersPerPR,
		SelfMergeRate:               selfMergeRate,
		SelfMergeWithApproval:       selfMergedWithApproval,
		SelfMergeWithoutApproval:    selfMergedWithoutApproval,
		MergeTypeTrend:              mergeTypeTrend,
		CommitFrequencyPerWeek:      commitFrequencyPerWeek,
		ReopenedPRs:                 reopenedPRs,
		ReopenRate:                  reopenRate,
		RevertLikeMerges:            revertLikeMerges,
		ReleaseCount:                releaseCount,

		// Comment timing metrics
		AverageTimeToFirstComment: avgTimeToFirstComment,
		MedianTimeToFirstComment:  medianTimeToFirstComment,
		AverageTimeToFirstReview:  avgTimeToFirstReview,
		MedianTimeToFirstReview:   medianTimeToFirstReview,
		AverageReviewResponseTime: avgReviewResponseTime,
		PRsWithComments:           prsWithComments,
		PRsWithReviews:            prsWithReviews,

		// Comment quantity metrics
		AverageCommentsPerPR: avgCommentsPerPR,
		MedianCommentsPerPR:  medianCommentsPerPR,
		CommentDensity:       commentDensity,
		MaxCommentsInPR:      maxComments,
		PRsWithoutComments:   prsWithoutComments,

		// Review comment metrics
		AverageReviewCommentsPerPR: avgReviewCommentsPerPR,
		MedianReviewCommentsPerPR:  medianReviewCommentsPerPR,
		MaxReviewCommentsInPR:      maxReviewComments,
		PRsWithReviewComments:      prsWithReviewComments,
		PRsWithoutReviewComments:   prsWithoutReviewComments,
	}
}
