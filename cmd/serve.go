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

type dashboardMetric struct {
	PRs              int     `json:"prs"`
	Releases         int     `json:"releases"`
	LeadTime         string  `json:"leadTime"`
	LeadTimeHours    float64 `json:"leadTimeHours"`
	ApprovalToMerge  string  `json:"approvalToMerge"`
	ApprovalHours    float64 `json:"approvalHours"`
	ReleaseToMain    string  `json:"releaseToMain"`
	ReleaseMainHours float64 `json:"releaseMainHours"`
	Hotfixes         int     `json:"hotfixes"`
	Reverts          int     `json:"reverts"`
}

type dashboardRow struct {
	Month      int              `json:"month"`
	Primary    dashboardMetric  `json:"primary"`
	Comparison *dashboardMetric `json:"comparison,omitempty"`
}

type dashboardReport struct {
	Repository     string         `json:"repository"`
	Author         string         `json:"author,omitempty"`
	Year           int            `json:"year"`
	ComparisonYear int            `json:"comparisonYear,omitempty"`
	GeneratedAt    string         `json:"generatedAt"`
	Rows           []dashboardRow `json:"rows"`
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
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, _ []string) error {
	interactive := serveInteractive || serveHasNoSelectionFlags(cmd)
	targetRepo, err := getTargetRepo()
	if err != nil {
		return err
	}

	year := time.Now().UTC().Year()
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

	comparison, hasComparison := 0, false
	if compareYear != "" || comparePrevYear {
		comparison, hasComparison, err = resolveComparisonYear(year, compareYear, comparePrevYear)
		if err != nil {
			return err
		}
	} else if interactive {
		comparison, hasComparison, err = promptServeComparison(year)
		if err != nil {
			return err
		}
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
	now := time.Now().UTC()
	monthLimit, partialMonth, err := trendRangeLimit(year, comparison, hasComparison, now)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", servePort))
	if err != nil {
		return fmt.Errorf("start dashboard: %w", err)
	}
	restoreSpinners := animation.SuppressSpinners()
	defer restoreSpinners()
	url := fmt.Sprintf("http://127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port)
	total := monthLimit
	if hasComparison {
		total *= 2
	}
	state := &dashboardState{status: "running", stage: "開始しています", total: total, started: time.Now()}
	go collectDashboard(state, targetRepo, year, comparison, hasComparison, monthLimit, partialMonth)
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
	for _, name := range []string{"repo", "year", "compare-year", "compare-prev-year", "port"} {
		if serveFlagChanged(cmd, name) {
			return false
		}
	}
	return true
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

func collectDashboard(state *dashboardState, targetRepo string, year, comparison int, hasComparison bool, monthLimit int, partialMonth bool) {
	now := time.Now().UTC()
	report := &dashboardReport{Repository: targetRepo, Author: author, Year: year, ComparisonYear: comparison,
		GeneratedAt: time.Now().Format(time.RFC3339), Rows: make([]dashboardRow, monthLimit)}
	for m := 1; m <= monthLimit; m++ {
		state.setStage(fmt.Sprintf("%d年%d月のPRを取得中", year, m))
		metric, err := fetchDashboardMetric(targetRepo, year, m, monthLimit, partialMonth, now.Day())
		if err != nil {
			state.fail(err)
			return
		}
		report.Rows[m-1] = dashboardRow{Month: m, Primary: metric}
		state.advance()
	}
	if hasComparison {
		for m := 1; m <= monthLimit; m++ {
			state.setStage(fmt.Sprintf("%d年%d月のPRを取得中", comparison, m))
			metric, err := fetchDashboardMetric(targetRepo, comparison, m, monthLimit, partialMonth, now.Day())
			if err != nil {
				state.fail(err)
				return
			}
			report.Rows[m-1].Comparison = &metric
			state.advance()
		}
	}
	state.complete(report)
}

func fetchDashboardMetric(targetRepo string, year, month, monthLimit int, partialMonth bool, currentDay int) (dashboardMetric, error) {
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0).Add(-time.Nanosecond)
	if partialMonth && month == monthLimit {
		end = comparableMonthEnd(year, time.Month(month), currentDay)
	}
	prs, err := github.FetchPullRequests(targetRepo, start.Format("2006-01-02"), end.Format("2006-01-02"), author, label, true)
	if err != nil {
		return dashboardMetric{}, err
	}
	return metricFromStats(stats.CalculateStats(CalculateLeadTimes(prs))), nil
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
	fmt.Fprintf(&b, "- Primary year: %d\n", report.Year)
	if report.ComparisonYear != 0 {
		fmt.Fprintf(&b, "- Comparison year: %d\n", report.ComparisonYear)
	}
	fmt.Fprintf(&b, "- Generated: %s\n- Dependabot PRs: excluded\n\n", report.GeneratedAt)
	fmt.Fprintf(&b, "| 月 | %d PR | %d リリース | %d Lead Time | %d Approval→Merge | %d Release→Main | %d Hotfix | %d Revert |", report.Year, report.Year, report.Year, report.Year, report.Year, report.Year, report.Year)
	if report.ComparisonYear != 0 {
		fmt.Fprintf(&b, " %d PR | %d リリース | %d Lead Time | %d Approval→Merge | %d Release→Main | %d Hotfix | %d Revert |", report.ComparisonYear, report.ComparisonYear, report.ComparisonYear, report.ComparisonYear, report.ComparisonYear, report.ComparisonYear, report.ComparisonYear)
	}
	b.WriteString("\n|---|" + strings.Repeat("---:|", 7))
	if report.ComparisonYear != 0 {
		b.WriteString(strings.Repeat("---:|", 7))
	}
	b.WriteString("\n")
	for _, row := range report.Rows {
		p := row.Primary
		fmt.Fprintf(&b, "| %d月 | %d | %d | %s | %s | %s | %d | %d |", row.Month, p.PRs, p.Releases, p.LeadTime, p.ApprovalToMerge, p.ReleaseToMain, p.Hotfixes, p.Reverts)
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
:root{color-scheme:dark;--bg:#0b1020;--panel:#151c31;--line:#2a3657;--text:#edf2ff;--muted:#9eabd0;--a:#70a5ff;--b:#f3ad61}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px system-ui,sans-serif}.wrap{max-width:1280px;margin:auto;padding:30px}header{display:flex;align-items:center;justify-content:space-between;gap:16px}h1{font-size:28px;margin:0}.sub,.muted,footer{color:var(--muted)}.actions{display:flex;gap:8px}button,.button{background:#243354;color:white;border:1px solid #40517a;border-radius:8px;padding:9px 13px;text-decoration:none;cursor:pointer}.panel{background:var(--panel);border:1px solid var(--line);border-radius:14px;padding:20px;margin-top:18px}.progress-head{display:flex;justify-content:space-between}.track{height:16px;background:#27304a;border-radius:10px;overflow:hidden;margin:12px 0}.bar{height:100%;width:0;background:linear-gradient(90deg,var(--a),#7ce3d0);transition:width .35s}.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:12px;margin-top:18px}.card{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:16px}.card b{display:block;font-size:25px;margin-top:8px}.charts{display:grid;grid-template-columns:1fr 1fr;gap:18px}canvas{width:100%;height:260px}.table-wrap{overflow:auto}table{border-collapse:collapse;width:100%;white-space:nowrap}th,td{border-bottom:1px solid var(--line);padding:10px;text-align:right}th:first-child,td:first-child{text-align:left}#dashboard{display:none}.error{color:#ff8e9d}footer{margin:20px 0}@media(max-width:800px){.charts{grid-template-columns:1fr}header{align-items:flex-start;flex-direction:column}}@media print{body{background:white;color:#111}.wrap{max-width:none;padding:0}.panel,.card{background:white;border-color:#bbb}.actions,#progress{display:none!important}.muted,.sub,footer{color:#555}}
</style></head><body><div class="wrap"><header><div><h1>RepoTrends</h1><div class="sub" id="subtitle">GitHub analytics dashboard</div></div><div class="actions" id="actions" style="display:none"><a class="button" href="/report.md">Markdown</a><button onclick="window.print()">印刷・PDF保存</button></div></header><section class="panel" id="progress"><div class="progress-head"><b id="stage">開始しています</b><span id="percent">0%</span></div><div class="track"><div class="bar" id="bar"></div></div><div class="muted"><span id="count">0 / 0</span> ・経過 <span id="elapsed">0s</span></div><div class="error" id="error"></div></section><main id="dashboard"><section class="cards" id="cards"></section><section class="charts"><div class="panel"><h3>PRリードタイム中央値（時間）</h3><canvas id="leadChart"></canvas></div><div class="panel"><h3>リリース回数</h3><canvas id="releaseChart"></canvas></div></section><section class="panel table-wrap"><h3>月次比較</h3><table id="table"></table></section></main><footer>データはlocalhost内で表示されます。Dependabot PRは除外されています。</footer></div><script>
const $=id=>document.getElementById(id);let rendered=false;async function poll(){try{const s=await fetch('/api/status',{cache:'no-store'}).then(r=>r.json()),pct=s.total?Math.round(s.completed/s.total*100):0;$('bar').style.width=pct+'%';$('percent').textContent=pct+'%';$('stage').textContent=s.stage;$('count').textContent=s.completed+' / '+s.total;$('elapsed').textContent=s.elapsed;if(s.error)$('error').textContent=s.error;if(s.status==='complete'&&!rendered){rendered=true;render(s.report)}if(s.status==='running')setTimeout(poll,700)}catch(e){$('error').textContent=e;setTimeout(poll,1500)}}poll();function total(rows,key,side='primary'){return rows.reduce((n,r)=>n+(r[side]?.[key]||0),0)}function render(r){$('progress').style.display='none';$('dashboard').style.display='block';$('actions').style.display='flex';$('subtitle').textContent=r.repository+(r.author?' ・ @'+r.author:' ・ 全Author')+' ・ '+r.year+(r.comparisonYear?' vs '+r.comparisonYear:'');const cards=[['対象PR',total(r.rows,'prs')],['リリース',total(r.rows,'releases')],['Hotfix',total(r.rows,'hotfixes')],['Revert',total(r.rows,'reverts')]];$('cards').innerHTML=cards.map(x=>'<div class="card"><span class="muted">'+x[0]+' ('+r.year+')</span><b>'+x[1]+'</b></div>').join('');draw('leadChart',r,'leadTimeHours');draw('releaseChart',r,'releases');table(r)}
function draw(id,r,key){const c=$(id),d=devicePixelRatio||1,w=c.clientWidth,h=260;c.width=w*d;c.height=h*d;const x=c.getContext('2d');x.scale(d,d);const values=r.rows.flatMap(v=>[v.primary[key],v.comparison?.[key]||0]),max=Math.max(...values,1);x.strokeStyle='#34415f';x.fillStyle='#9eabd0';x.font='12px system-ui';for(let i=0;i<5;i++){const y=20+(h-55)*i/4;x.beginPath();x.moveTo(35,y);x.lineTo(w-10,y);x.stroke();x.fillText(Math.round(max*(4-i)/4),4,y+4)}line(r.rows.map(v=>v.primary[key]),'#70a5ff',r.year,18);if(r.comparisonYear)line(r.rows.map(v=>v.comparison?.[key]||0),'#f3ad61',r.comparisonYear,35);function line(a,color,label,ly){x.strokeStyle=color;x.lineWidth=2;x.beginPath();a.forEach((v,i)=>{const px=42+(w-62)*(i/Math.max(a.length-1,1)),py=20+(h-55)*(1-v/max);i?x.lineTo(px,py):x.moveTo(px,py);x.fillStyle=color;x.fillRect(px-3,py-3,6,6);x.fillStyle='#9eabd0';x.fillText((i+1)+'月',px-10,h-12)});x.stroke();x.fillStyle=color;x.fillText(label,w-70,ly)}}function table(r){let h='<thead><tr><th>月</th>'+head(r.year);if(r.comparisonYear)h+=head(r.comparisonYear);h+='</tr></thead><tbody>';for(const row of r.rows){h+='<tr><td>'+row.month+'月</td>'+cells(row.primary);if(row.comparison)h+=cells(row.comparison);h+='</tr>'}h+='</tbody>';$('table').innerHTML=h}function head(y){return '<th>'+y+' PR</th><th>'+y+' リリース</th><th>'+y+' LeadTime</th><th>'+y+' Approval→Merge</th><th>'+y+' Release→Main</th><th>'+y+' Hotfix</th><th>'+y+' Revert</th>'}function cells(v){return '<td>'+v.prs+'</td><td>'+v.releases+'</td><td>'+v.leadTime+'</td><td>'+v.approvalToMerge+'</td><td>'+v.releaseToMain+'</td><td>'+v.hotfixes+'</td><td>'+v.reverts+'</td>'}
</script></body></html>`
