package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hironeko/gh-trends/internal/animation"
	"github.com/hironeko/gh-trends/internal/github"
	"github.com/hironeko/gh-trends/internal/stats"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var servePort int
var serveNoOpen bool
var serveInteractive bool
var serveMonth int
var serveMonths int
var serveComparePrevMonth bool

type dashboardMonthSelection struct {
	Date       time.Time
	PartialDay int
}

type dashboardPlan struct {
	Primary         []dashboardMonthSelection
	Comparison      []dashboardMonthSelection
	PrimaryLabel    string
	ComparisonLabel string
}

type dashboardMetric struct {
	PRs              int            `json:"prs"`
	Releases         int            `json:"releases"`
	LeadTime         string         `json:"leadTime"`
	LeadTimeHours    float64        `json:"leadTimeHours"`
	ApprovalToMerge  string         `json:"approvalToMerge"`
	ApprovalHours    float64        `json:"approvalHours"`
	ReleaseToMain    string         `json:"releaseToMain"`
	ReleaseMainHours float64        `json:"releaseMainHours"`
	Hotfixes         int            `json:"hotfixes"`
	Reverts          int            `json:"reverts"`
	DailyPRs         map[string]int `json:"dailyPRs,omitempty"`
}

type dashboardRow struct {
	Month           int              `json:"month"`
	Label           string           `json:"label"`
	ComparisonLabel string           `json:"comparisonLabel,omitempty"`
	Primary         dashboardMetric  `json:"primary"`
	Comparison      *dashboardMetric `json:"comparison,omitempty"`
}

type dashboardReport struct {
	Repository      string         `json:"repository"`
	Author          string         `json:"author,omitempty"`
	Year            int            `json:"year"`
	ComparisonYear  int            `json:"comparisonYear,omitempty"`
	PrimaryLabel    string         `json:"primaryLabel"`
	ComparisonLabel string         `json:"comparisonLabel,omitempty"`
	GeneratedAt     string         `json:"generatedAt"`
	Rows            []dashboardRow `json:"rows"`
}

type dashboardSnapshot struct {
	Status    string           `json:"status"`
	Stage     string           `json:"stage"`
	Completed int              `json:"completed"`
	Total     int              `json:"total"`
	Error     string           `json:"error,omitempty"`
	Elapsed   string           `json:"elapsed"`
	Report    *dashboardReport `json:"report,omitempty"`
}

type dashboardState struct {
	mu        sync.RWMutex
	status    string
	stage     string
	completed int
	total     int
	err       string
	started   time.Time
	report    *dashboardReport
}

func (s *dashboardState) snapshot() dashboardSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return dashboardSnapshot{Status: s.status, Stage: s.stage, Completed: s.completed, Total: s.total,
		Error: s.err, Elapsed: time.Since(s.started).Round(time.Second).String(), Report: s.report}
}

func (s *dashboardState) setStage(stage string) {
	s.mu.Lock()
	s.stage = stage
	s.mu.Unlock()
}

func (s *dashboardState) advance() {
	s.mu.Lock()
	s.completed++
	s.mu.Unlock()
}

func (s *dashboardState) fail(err error) {
	s.mu.Lock()
	s.status, s.err = "error", err.Error()
	s.mu.Unlock()
}

func (s *dashboardState) complete(report *dashboardReport) {
	s.mu.Lock()
	s.status, s.stage, s.report = "complete", "取得完了", report
	s.mu.Unlock()
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Open a local analytics dashboard",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 8080, "Local dashboard port")
	serveCmd.Flags().BoolVar(&serveNoOpen, "no-open", false, "Do not open the browser automatically")
	serveCmd.Flags().BoolVarP(&serveInteractive, "interactive", "i", false, "Prompt for dashboard options")
	serveCmd.Flags().IntVar(&serveMonth, "month", 0, "End month to analyze (1-12; use with --year)")
	serveCmd.Flags().IntVar(&serveMonths, "months", 1, "Number of months to show, ending at --month")
	serveCmd.Flags().BoolVar(&serveComparePrevMonth, "compare-prev-month", false, "Compare each month against its previous month")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, _ []string) error {
	interactive := serveInteractive || serveHasNoSelectionFlags(cmd)
	targetRepo, err := getTargetRepo()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	monthMode := serveMonth != 0 || serveFlagChanged(cmd, "months") || serveComparePrevMonth
	if interactive && serveMonth == 0 && !serveFlagChanged(cmd, "months") && !serveComparePrevMonth {
		monthMode, err = promptServePeriodMode()
		if err != nil {
			return err
		}
	}

	year := now.Year()
	if yearTrend != "" {
		year, err = strconv.Atoi(yearTrend)
		if err != nil || year < 2000 {
			return fmt.Errorf("invalid --year value")
		}
	} else if interactive {
		year, err = promptServeYear(year)
		if err != nil {
			return err
		}
	}

	if monthMode {
		if serveMonth == 0 {
			if interactive {
				serveMonth, err = promptServeMonth(year, now)
				if err != nil {
					return err
				}
			} else {
				serveMonth = int(now.Month())
			}
		}
		if interactive && !serveFlagChanged(cmd, "months") {
			serveMonths, err = promptServeMonthCount(serveMonths)
			if err != nil {
				return err
			}
		}
	}

	comparisonMode, comparison, err := resolveServeComparison(monthMode, year, interactive)
	if err != nil {
		return err
	}
	plan, err := buildDashboardPlan(year, monthMode, serveMonth, serveMonths, comparisonMode, comparison, now)
	if err != nil {
		return err
	}

	if interactive && author == "" {
		author, err = promptServeAuthor()
		if err != nil {
			return err
		}
	}
	if interactive && !serveFlagChanged(cmd, "port") {
		servePort, err = promptServePort(servePort)
		if err != nil {
			return err
		}
	}

	if err := github.ValidateRepository(targetRepo); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", servePort))
	if err != nil {
		return fmt.Errorf("start dashboard: %w", err)
	}
	restoreSpinners := animation.SuppressSpinners()
	defer restoreSpinners()
	url := fmt.Sprintf("http://127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port)
	total := len(plan.Primary) + len(plan.Comparison)
	state := &dashboardState{status: "running", stage: "開始しています", total: total, started: time.Now()}
	go collectDashboard(state, targetRepo, year, comparison, plan)
	if !serveNoOpen {
		go openBrowser(url)
	}
	fmt.Printf("RepoTrends dashboard: %s\n終了するには Ctrl+C を押してください。\n", url)
	return http.Serve(listener, dashboardMux(state))
}

func serveFlagChanged(cmd *cobra.Command, name string) bool {
	if flag := cmd.Flags().Lookup(name); flag != nil {
		return flag.Changed
	}
	if flag := cmd.InheritedFlags().Lookup(name); flag != nil {
		return flag.Changed
	}
	return false
}

func serveHasNoSelectionFlags(cmd *cobra.Command) bool {
	for _, name := range []string{"repo", "year", "month", "months", "compare-year", "compare-prev-year", "compare-prev-month", "port"} {
		if serveFlagChanged(cmd, name) {
			return false
		}
	}
	return true
}

func promptServePeriodMode() (bool, error) {
	prompt := promptui.Select{Label: "集計単位", Items: []string{"年単位", "月単位"}}
	index, _, err := prompt.Run()
	if err != nil {
		return false, fmt.Errorf("集計単位の選択に失敗しました: %w", err)
	}
	return index == 1, nil
}

func promptServeMonth(year int, now time.Time) (int, error) {
	defaultMonth := int(now.Month())
	prompt := promptui.Prompt{
		Label:   "対象月（1〜12）",
		Default: strconv.Itoa(defaultMonth),
		Validate: func(input string) error {
			month, err := strconv.Atoi(input)
			if err != nil || month < 1 || month > 12 {
				return fmt.Errorf("1〜12の範囲で入力してください")
			}
			if time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC).After(firstOfMonth(now)) {
				return fmt.Errorf("未来の月は指定できません")
			}
			return nil
		},
	}
	value, err := prompt.Run()
	if err != nil {
		return 0, fmt.Errorf("対象月の入力に失敗しました: %w", err)
	}
	return strconv.Atoi(value)
}

func promptServeMonthCount(defaultCount int) (int, error) {
	prompt := promptui.Prompt{
		Label:   "表示する月数",
		Default: strconv.Itoa(defaultCount),
		Validate: func(input string) error {
			count, err := strconv.Atoi(input)
			if err != nil || count < 1 || count > 120 {
				return fmt.Errorf("1〜120か月の範囲で入力してください")
			}
			return nil
		},
	}
	value, err := prompt.Run()
	if err != nil {
		return 0, fmt.Errorf("月数の入力に失敗しました: %w", err)
	}
	return strconv.Atoi(value)
}

func resolveServeComparison(monthMode bool, year int, interactive bool) (string, int, error) {
	selected := 0
	if compareYear != "" {
		selected++
	}
	if comparePrevYear {
		selected++
	}
	if serveComparePrevMonth {
		selected++
	}
	if selected > 1 {
		return "", 0, fmt.Errorf("比較方法は --compare-year、--compare-prev-year、--compare-prev-month のいずれか1つを指定してください")
	}
	if serveComparePrevMonth {
		if !monthMode {
			return "", 0, fmt.Errorf("--compare-prev-month は月単位の集計で使用してください")
		}
		return "previous-month", 0, nil
	}
	if comparePrevYear {
		return "previous-year", year - 1, nil
	}
	if compareYear != "" {
		comparison, err := strconv.Atoi(compareYear)
		if err != nil || comparison < 2000 || comparison == year {
			return "", 0, fmt.Errorf("invalid --compare-year value")
		}
		return "specified-year", comparison, nil
	}
	if !interactive {
		return "", 0, nil
	}
	if !monthMode {
		comparison, hasComparison, err := promptServeComparison(year)
		if err != nil || !hasComparison {
			return "", 0, err
		}
		if comparison == year-1 {
			return "previous-year", comparison, nil
		}
		return "specified-year", comparison, nil
	}

	prompt := promptui.Select{
		Label: "比較対象",
		Items: []string{"比較しない", "前月", fmt.Sprintf("前年同月（%d年）", year-1), "指定年の同月"},
	}
	index, _, err := prompt.Run()
	if err != nil {
		return "", 0, fmt.Errorf("比較対象の選択に失敗しました: %w", err)
	}
	switch index {
	case 0:
		return "", 0, nil
	case 1:
		return "previous-month", 0, nil
	case 2:
		return "previous-year", year - 1, nil
	default:
		comparison, _, err := promptServeComparisonYear(year)
		return "specified-year", comparison, err
	}
}

func promptServeComparisonYear(year int) (int, bool, error) {
	prompt := promptui.Prompt{
		Label:   "比較する年",
		Default: strconv.Itoa(year - 1),
		Validate: func(input string) error {
			comparison, err := strconv.Atoi(input)
			if err != nil || comparison < 2000 || comparison == year {
				return fmt.Errorf("対象年とは異なる2000年以降の年を入力してください")
			}
			return nil
		},
	}
	value, err := prompt.Run()
	if err != nil {
		return 0, false, fmt.Errorf("比較年の入力に失敗しました: %w", err)
	}
	comparison, _ := strconv.Atoi(value)
	return comparison, true, nil
}

func firstOfMonth(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func sameMonth(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month()
}

func periodLabel(months []dashboardMonthSelection) string {
	if len(months) == 0 {
		return ""
	}
	first := months[0].Date
	last := months[len(months)-1].Date
	if sameMonth(first, last) {
		return fmt.Sprintf("%d年%d月", first.Year(), first.Month())
	}
	return fmt.Sprintf("%d年%d月〜%d年%d月", first.Year(), first.Month(), last.Year(), last.Month())
}

func buildDashboardPlan(year int, monthMode bool, month, count int, comparisonMode string, comparisonYear int, now time.Time) (dashboardPlan, error) {
	nowMonth := firstOfMonth(now)
	plan := dashboardPlan{}
	if !monthMode {
		hasComparison := comparisonMode != ""
		monthLimit, partial, err := trendRangeLimit(year, comparisonYear, hasComparison, now)
		if err != nil {
			return plan, err
		}
		for index := 1; index <= monthLimit; index++ {
			selection := dashboardMonthSelection{Date: time.Date(year, time.Month(index), 1, 0, 0, 0, 0, time.UTC)}
			if partial && index == monthLimit {
				selection.PartialDay = now.Day()
			}
			plan.Primary = append(plan.Primary, selection)
			if hasComparison {
				comparison := dashboardMonthSelection{Date: time.Date(comparisonYear, time.Month(index), 1, 0, 0, 0, 0, time.UTC), PartialDay: selection.PartialDay}
				plan.Comparison = append(plan.Comparison, comparison)
			}
		}
		plan.PrimaryLabel = strconv.Itoa(year)
		if hasComparison {
			plan.ComparisonLabel = strconv.Itoa(comparisonYear)
		}
		return plan, nil
	}

	if month < 1 || month > 12 {
		return plan, fmt.Errorf("--month must be between 1 and 12")
	}
	if count < 1 || count > 120 {
		return plan, fmt.Errorf("--months must be between 1 and 120")
	}
	target := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	if target.After(nowMonth) {
		return plan, fmt.Errorf("future months cannot be analyzed")
	}
	start := target.AddDate(0, -(count - 1), 0)
	if start.Year() < 2000 {
		return plan, fmt.Errorf("monthly range must start in 2000 or later")
	}
	for index := 0; index < count; index++ {
		date := start.AddDate(0, index, 0)
		selection := dashboardMonthSelection{Date: date}
		if sameMonth(date, nowMonth) {
			selection.PartialDay = now.Day()
		}
		plan.Primary = append(plan.Primary, selection)

		var comparisonDate time.Time
		switch comparisonMode {
		case "previous-month":
			comparisonDate = date.AddDate(0, -1, 0)
		case "previous-year":
			comparisonDate = date.AddDate(-1, 0, 0)
		case "specified-year":
			comparisonDate = date.AddDate(comparisonYear-year, 0, 0)
		case "":
			continue
		default:
			return plan, fmt.Errorf("unknown comparison mode %q", comparisonMode)
		}
		if comparisonDate.After(nowMonth) {
			return plan, fmt.Errorf("comparison period cannot be in the future")
		}
		comparisonSelection := dashboardMonthSelection{Date: comparisonDate}
		if selection.PartialDay > 0 || sameMonth(comparisonDate, nowMonth) {
			selection.PartialDay = now.Day()
			plan.Primary[len(plan.Primary)-1].PartialDay = now.Day()
			comparisonSelection.PartialDay = now.Day()
		}
		plan.Comparison = append(plan.Comparison, comparisonSelection)
	}
	plan.PrimaryLabel = periodLabel(plan.Primary)
	plan.ComparisonLabel = periodLabel(plan.Comparison)
	return plan, nil
}

func promptServeYear(defaultYear int) (int, error) {
	prompt := promptui.Prompt{
		Label:   "対象年",
		Default: strconv.Itoa(defaultYear),
		Validate: func(input string) error {
			year, err := strconv.Atoi(input)
			if err != nil || year < 2000 {
				return fmt.Errorf("2000年以降を4桁で入力してください")
			}
			return nil
		},
	}
	value, err := prompt.Run()
	if err != nil {
		return 0, fmt.Errorf("対象年の入力に失敗しました: %w", err)
	}
	return strconv.Atoi(value)
}

func promptServeComparison(year int) (int, bool, error) {
	selectPrompt := promptui.Select{
		Label: "比較対象",
		Items: []string{
			fmt.Sprintf("前年（%d年）", year-1),
			"比較しない",
			"年を指定する",
		},
	}
	index, _, err := selectPrompt.Run()
	if err != nil {
		return 0, false, fmt.Errorf("比較対象の選択に失敗しました: %w", err)
	}
	if index == 0 {
		return year - 1, true, nil
	}
	if index == 1 {
		return 0, false, nil
	}

	prompt := promptui.Prompt{
		Label:   "比較する年",
		Default: strconv.Itoa(year - 1),
		Validate: func(input string) error {
			comparison, err := strconv.Atoi(input)
			if err != nil || comparison < 2000 || comparison == year {
				return fmt.Errorf("対象年とは異なる2000年以降の年を入力してください")
			}
			return nil
		},
	}
	value, err := prompt.Run()
	if err != nil {
		return 0, false, fmt.Errorf("比較年の入力に失敗しました: %w", err)
	}
	comparison, _ := strconv.Atoi(value)
	return comparison, true, nil
}

func promptServePort(defaultPort int) (int, error) {
	prompt := promptui.Prompt{
		Label:   "ポート番号",
		Default: strconv.Itoa(defaultPort),
		Validate: func(input string) error {
			port, err := strconv.Atoi(input)
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("1〜65535の範囲で入力してください")
			}
			return nil
		},
	}
	value, err := prompt.Run()
	if err != nil {
		return 0, fmt.Errorf("ポート番号の入力に失敗しました: %w", err)
	}
	return strconv.Atoi(value)
}

func promptServeAuthor() (string, error) {
	selectPrompt := promptui.Select{
		Label: "分析対象",
		Items: []string{"リポジトリ全体", "Authorを指定する"},
	}
	index, _, err := selectPrompt.Run()
	if err != nil {
		return "", fmt.Errorf("分析対象の選択に失敗しました: %w", err)
	}
	if index == 0 {
		return "", nil
	}
	prompt := promptui.Prompt{
		Label: "GitHubアカウント名",
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" || strings.ContainsAny(input, " /@") {
				return fmt.Errorf("@を付けずにGitHubアカウント名を入力してください")
			}
			return nil
		},
	}
	value, err := prompt.Run()
	if err != nil {
		return "", fmt.Errorf("GitHubアカウント名の入力に失敗しました: %w", err)
	}
	return strings.TrimSpace(value), nil
}

func collectDashboard(state *dashboardState, targetRepo string, year, comparison int, plan dashboardPlan) {
	report := &dashboardReport{Repository: targetRepo, Author: author, Year: year, ComparisonYear: comparison,
		PrimaryLabel: plan.PrimaryLabel, ComparisonLabel: plan.ComparisonLabel,
		GeneratedAt: time.Now().Format(time.RFC3339), Rows: make([]dashboardRow, len(plan.Primary))}
	for index, selection := range plan.Primary {
		state.setStage(fmt.Sprintf("%d年%d月のPRを取得中", selection.Date.Year(), selection.Date.Month()))
		metric, err := fetchDashboardMetric(targetRepo, selection)
		if err != nil {
			state.fail(err)
			return
		}
		report.Rows[index] = dashboardRow{
			Month: int(selection.Date.Month()), Label: fmt.Sprintf("%d/%02d", selection.Date.Year(), selection.Date.Month()), Primary: metric,
		}
		state.advance()
	}
	if len(plan.Comparison) > 0 {
		for index, selection := range plan.Comparison {
			state.setStage(fmt.Sprintf("%d年%d月のPRを取得中", selection.Date.Year(), selection.Date.Month()))
			metric, err := fetchDashboardMetric(targetRepo, selection)
			if err != nil {
				state.fail(err)
				return
			}
			report.Rows[index].ComparisonLabel = fmt.Sprintf("%d/%02d", selection.Date.Year(), selection.Date.Month())
			report.Rows[index].Comparison = &metric
			state.advance()
		}
	}
	state.complete(report)
}

func fetchDashboardMetric(targetRepo string, selection dashboardMonthSelection) (dashboardMetric, error) {
	start := selection.Date
	end := start.AddDate(0, 1, 0).Add(-time.Nanosecond)
	if selection.PartialDay > 0 {
		end = comparableMonthEnd(start.Year(), start.Month(), selection.PartialDay)
	}
	prs, err := github.FetchPullRequests(targetRepo, start.Format("2006-01-02"), end.Format("2006-01-02"), author, label, true)
	if err != nil {
		return dashboardMetric{}, err
	}
	metric := metricFromStats(stats.CalculateStats(CalculateLeadTimes(prs)))
	metric.DailyPRs = dailyPRCounts(prs, start, end)
	return metric, nil
}

func dailyPRCounts(prs []github.PullRequest, start, end time.Time) map[string]int {
	counts := make(map[string]int)
	for _, pr := range prs {
		created := pr.CreatedAt.UTC()
		if created.Before(start) || created.After(end) {
			continue
		}
		counts[created.Format("2006-01-02")]++
	}
	return counts
}

func metricFromStats(s stats.Stats) dashboardMetric {
	return dashboardMetric{PRs: s.TotalPRs, Releases: s.ReleaseCount,
		LeadTime: formatDuration(s.MedianLeadTime), LeadTimeHours: s.MedianLeadTime.Hours(),
		ApprovalToMerge: formatDuration(s.MedianApprovalToMerge), ApprovalHours: s.MedianApprovalToMerge.Hours(),
		ReleaseToMain: formatDuration(s.MedianReleaseToMain), ReleaseMainHours: s.MedianReleaseToMain.Hours(),
		Hotfixes: s.HotfixMerges, Reverts: s.RevertLikeMerges}
}

func dashboardMux(state *dashboardState) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(dashboardHTML))
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(state.snapshot())
	})
	mux.HandleFunc("/report.md", func(w http.ResponseWriter, _ *http.Request) {
		snapshot := state.snapshot()
		if snapshot.Report == nil {
			http.Error(w, "report is not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="repo-trends-report.md"`)
		_, _ = w.Write([]byte(markdownReport(snapshot.Report)))
	})
	return mux
}

func markdownReport(report *dashboardReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# RepoTrends Report\n\n- Repository: `%s`\n", report.Repository)
	if report.Author != "" {
		fmt.Fprintf(&b, "- Author: `@%s`\n", report.Author)
	} else {
		b.WriteString("- Author: All authors\n")
	}
	fmt.Fprintf(&b, "- Primary period: %s\n", report.PrimaryLabel)
	if report.ComparisonLabel != "" {
		fmt.Fprintf(&b, "- Comparison period: %s\n", report.ComparisonLabel)
	}
	fmt.Fprintf(&b, "- Generated: %s\n- Dependabot PRs: excluded\n\n", report.GeneratedAt)
	fmt.Fprintf(&b, "| 月 | %s PR | %s リリース | %s Lead Time | %s Approval→Merge | %s Release→Main | %s Hotfix | %s Revert |", report.PrimaryLabel, report.PrimaryLabel, report.PrimaryLabel, report.PrimaryLabel, report.PrimaryLabel, report.PrimaryLabel, report.PrimaryLabel)
	if report.ComparisonLabel != "" {
		fmt.Fprintf(&b, " %s PR | %s リリース | %s Lead Time | %s Approval→Merge | %s Release→Main | %s Hotfix | %s Revert |", report.ComparisonLabel, report.ComparisonLabel, report.ComparisonLabel, report.ComparisonLabel, report.ComparisonLabel, report.ComparisonLabel, report.ComparisonLabel)
	}
	b.WriteString("\n|---|" + strings.Repeat("---:|", 7))
	if report.ComparisonLabel != "" {
		b.WriteString(strings.Repeat("---:|", 7))
	}
	b.WriteString("\n")
	for _, row := range report.Rows {
		p := row.Primary
		rowLabel := row.Label
		if row.ComparisonLabel != "" {
			rowLabel += " ↔ " + row.ComparisonLabel
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %s | %s | %d | %d |", rowLabel, p.PRs, p.Releases, p.LeadTime, p.ApprovalToMerge, p.ReleaseToMain, p.Hotfixes, p.Reverts)
		if row.Comparison != nil {
			c := row.Comparison
			fmt.Fprintf(&b, " %d | %d | %s | %s | %s | %d | %d |", c.PRs, c.Releases, c.LeadTime, c.ApprovalToMerge, c.ReleaseToMain, c.Hotfixes, c.Reverts)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n> Release→Mainは `release/*` から `main/master` へのPR作成からマージまでの中央値です。\n")
	return b.String()
}

func openBrowser(url string) {
	time.Sleep(250 * time.Millisecond)
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	_ = command.Start()
}

const dashboardHTML = `<!doctype html><html lang="ja"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>RepoTrends</title><style>
.heatmap .day,.legend .day{background:#161b22;box-shadow:inset 0 0 0 1px #30363d}.heatmap .day[data-level="1"],.legend .day[data-level="1"]{background:#0e4429;box-shadow:none}.heatmap .day[data-level="2"],.legend .day[data-level="2"]{background:#006d32;box-shadow:none}.heatmap .day[data-level="3"],.legend .day[data-level="3"]{background:#26a641;box-shadow:none}.heatmap .day[data-level="4"],.legend .day[data-level="4"]{background:#39d353;box-shadow:none}
:root{color-scheme:dark;--bg:#0b1020;--panel:#151c31;--line:#2a3657;--text:#edf2ff;--muted:#9eabd0;--a:#70a5ff;--b:#f3ad61}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px system-ui,sans-serif}.wrap{max-width:1280px;margin:auto;padding:30px}header{display:flex;align-items:center;justify-content:space-between;gap:16px}h1{font-size:28px;margin:0}.sub,.muted,footer{color:var(--muted)}.actions{display:flex;gap:8px}button,.button{background:#243354;color:white;border:1px solid #40517a;border-radius:8px;padding:9px 13px;text-decoration:none;cursor:pointer}.panel{background:var(--panel);border:1px solid var(--line);border-radius:14px;padding:20px;margin-top:18px}.progress-head,.activity-head{display:flex;justify-content:space-between;gap:16px;align-items:center}.track{height:16px;background:#27304a;border-radius:10px;overflow:hidden;margin:12px 0}.bar{height:100%;width:0;background:linear-gradient(90deg,var(--a),#7ce3d0);transition:width .35s}.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:12px;margin-top:18px}.card{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:16px}.card b{display:block;font-size:25px;margin-top:8px}.activity-scroll{overflow-x:auto;padding:14px 0 4px}.activity-layout{display:flex;gap:8px;min-width:max-content}.weekdays{display:grid;grid-template-rows:repeat(7,12px);gap:3px;color:var(--muted);font-size:9px;line-height:12px}.heatmap{display:grid;grid-template-rows:repeat(7,12px);grid-auto-flow:column;grid-auto-columns:12px;gap:3px}.day{width:12px;height:12px;border-radius:2px;background:#222b40}.day.outside{opacity:.25}.day[data-level="1"]{background:#0e4429}.day[data-level="2"]{background:#006d32}.day[data-level="3"]{background:#26a641}.day[data-level="4"]{background:#39d353}.legend{display:flex;align-items:center;gap:4px;color:var(--muted);font-size:11px}.legend .day{display:inline-block}.charts{display:grid;grid-template-columns:1fr 1fr;gap:18px}canvas{width:100%;height:260px}.table-wrap{overflow:auto}table{border-collapse:collapse;width:100%;white-space:nowrap}th,td{border-bottom:1px solid var(--line);padding:10px;text-align:right}th:first-child,td:first-child{text-align:left}#dashboard{display:none}.error{color:#ff8e9d}footer{margin:20px 0}@media(max-width:800px){.charts{grid-template-columns:1fr}header,.activity-head{align-items:flex-start;flex-direction:column}}@media print{body{background:white;color:#111}.wrap{max-width:none;padding:0}.panel,.card{background:white;border-color:#bbb}.actions,#progress{display:none!important}.muted,.sub,footer{color:#555}}
</style></head><body><div class="wrap"><header><div><h1>RepoTrends</h1><div class="sub" id="subtitle">GitHub analytics dashboard</div></div><div class="actions" id="actions" style="display:none"><a class="button" href="/report.md">Markdown</a><button onclick="window.print()">印刷・PDF保存</button></div></header><section class="panel" id="progress"><div class="progress-head"><b id="stage">開始しています</b><span id="percent">0%</span></div><div class="track"><div class="bar" id="bar"></div></div><div class="muted"><span id="count">0 / 0</span> ・経過 <span id="elapsed">0s</span></div><div class="error" id="error"></div></section><main id="dashboard"><section class="cards" id="cards"></section><section class="panel"><div class="activity-head"><div><h3 id="activityTitle" style="margin:0 0 5px">PR Activity</h3><span class="muted" id="activitySummary"></span></div><div class="legend"><span>Less</span><i class="day"></i><i class="day" data-level="1"></i><i class="day" data-level="2"></i><i class="day" data-level="3"></i><i class="day" data-level="4"></i><span>More</span></div></div><div class="activity-scroll"><div class="activity-layout"><div class="weekdays"><span></span><span>Mon</span><span></span><span>Wed</span><span></span><span>Fri</span><span></span></div><div class="heatmap" id="heatmap"></div></div></div></section><section class="charts"><div class="panel"><h3>PRリードタイム中央値（時間）</h3><canvas id="leadChart"></canvas></div><div class="panel"><h3>リリース回数</h3><canvas id="releaseChart"></canvas></div></section><section class="panel table-wrap"><h3>月次比較</h3><table id="table"></table></section></main><footer>データはlocalhost内で表示されます。Dependabot PRは除外されています。</footer></div><script>
const $=id=>document.getElementById(id);let rendered=false;async function poll(){try{const s=await fetch('/api/status',{cache:'no-store'}).then(r=>r.json()),pct=s.total?Math.round(s.completed/s.total*100):0;$('bar').style.width=pct+'%';$('percent').textContent=pct+'%';$('stage').textContent=s.stage;$('count').textContent=s.completed+' / '+s.total;$('elapsed').textContent=s.elapsed;if(s.error)$('error').textContent=s.error;if(s.status==='complete'&&!rendered){rendered=true;render(s.report)}if(s.status==='running')setTimeout(poll,700)}catch(e){$('error').textContent=e;setTimeout(poll,1500)}}poll();function total(rows,key,side='primary'){return rows.reduce((n,r)=>n+(r[side]?.[key]||0),0)}function render(r){$('progress').style.display='none';$('dashboard').style.display='block';$('actions').style.display='flex';$('subtitle').textContent=r.repository+(r.author?' ・ @'+r.author:' ・ 全Author')+' ・ '+r.primaryLabel+(r.comparisonLabel?' vs '+r.comparisonLabel:'');const cards=[['対象PR',total(r.rows,'prs')],['リリース',total(r.rows,'releases')],['Hotfix',total(r.rows,'hotfixes')],['Revert',total(r.rows,'reverts')]];$('cards').innerHTML=cards.map(x=>'<div class="card"><span class="muted">'+x[0]+' ('+r.primaryLabel+')</span><b>'+x[1]+'</b></div>').join('');heatmap(r);draw('leadChart',r,'leadTimeHours');draw('releaseChart',r,'releases');table(r)}
function heatmap(r){const daily={};for(const row of r.rows)Object.assign(daily,row.primary.dailyPRs||{});const dates=Object.keys(daily),totalPRs=dates.reduce((n,d)=>n+daily[d],0),max=Math.max(...Object.values(daily),1);$('activityTitle').textContent=r.author?'PR Contributions — @'+r.author:'Repository PR Activity';$('activitySummary').textContent=totalPRs+' PRs opened across '+dates.length+' active days · '+r.primaryLabel;const first=r.rows[0].label.split('/').map(Number),last=r.rows[r.rows.length-1].label.split('/').map(Number),start=new Date(Date.UTC(first[0],first[1]-1,1)),end=new Date(Date.UTC(last[0],last[1],0)),today=new Date(),currentEnd=new Date(Date.UTC(today.getFullYear(),today.getMonth(),today.getDate()));if(end>currentEnd)end.setTime(currentEnd.getTime());const cursor=new Date(start);cursor.setUTCDate(cursor.getUTCDate()-cursor.getUTCDay());const grid=$('heatmap');grid.innerHTML='';for(;cursor<=end;cursor.setUTCDate(cursor.getUTCDate()+1)){const key=cursor.toISOString().slice(0,10),count=daily[key]||0,cell=document.createElement('i');cell.className='day'+(cursor<start?' outside':'');const level=count?Math.max(1,Math.ceil(count/max*4)):0;cell.dataset.level=level;cell.title=key+': '+count+' PR'+(count===1?'':'s');grid.appendChild(cell)}}
function draw(id,r,key){const c=$(id),d=devicePixelRatio||1,w=c.clientWidth,h=260;c.width=w*d;c.height=h*d;const x=c.getContext('2d');x.scale(d,d);const values=r.rows.flatMap(v=>[v.primary[key],v.comparison?.[key]||0]),max=Math.max(...values,1);x.strokeStyle='#34415f';x.fillStyle='#9eabd0';x.font='12px system-ui';for(let i=0;i<5;i++){const y=20+(h-55)*i/4;x.beginPath();x.moveTo(35,y);x.lineTo(w-10,y);x.stroke();x.fillText(Math.round(max*(4-i)/4),4,y+4)}line(r.rows.map(v=>v.primary[key]),'#70a5ff',r.primaryLabel,18);if(r.comparisonLabel)line(r.rows.map(v=>v.comparison?.[key]||0),'#f3ad61',r.comparisonLabel,35);function line(a,color,label,ly){x.strokeStyle=color;x.lineWidth=2;x.beginPath();a.forEach((v,i)=>{const px=42+(w-62)*(i/Math.max(a.length-1,1)),py=20+(h-55)*(1-v/max);i?x.lineTo(px,py):x.moveTo(px,py);x.fillStyle=color;x.fillRect(px-3,py-3,6,6);x.fillStyle='#9eabd0';x.fillText(r.rows[i].label.slice(5),px-10,h-12)});x.stroke();x.fillStyle=color;x.fillText(label,w-150,ly)}}function table(r){let h='<thead><tr><th>月</th>'+head(r.primaryLabel);if(r.comparisonLabel)h+=head(r.comparisonLabel);h+='</tr></thead><tbody>';for(const row of r.rows){const label=row.comparisonLabel?row.label+' ↔ '+row.comparisonLabel:row.label;h+='<tr><td>'+label+'</td>'+cells(row.primary);if(row.comparison)h+=cells(row.comparison);h+='</tr>'}h+='</tbody>';$('table').innerHTML=h}function head(y){return '<th>'+y+' PR</th><th>'+y+' リリース</th><th>'+y+' LeadTime</th><th>'+y+' Approval→Merge</th><th>'+y+' Release→Main</th><th>'+y+' Hotfix</th><th>'+y+' Revert</th>'}function cells(v){return '<td>'+v.prs+'</td><td>'+v.releases+'</td><td>'+v.leadTime+'</td><td>'+v.approvalToMerge+'</td><td>'+v.releaseToMain+'</td><td>'+v.hotfixes+'</td><td>'+v.reverts+'</td>'}
</script></body></html>`
