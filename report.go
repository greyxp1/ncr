package main

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type reportRow struct {
	Name           string
	System         string
	ClosureBytes   int64
	Paths          int
	EvalTime       time.Duration
	Skipped        bool
	EvalPending    bool
	ClosurePending bool
}

type reportSection struct {
	Kind configurationKind
	Rows []reportRow
}

type liveReport struct {
	mu            sync.Mutex
	kinds         []configurationKind
	names         groupedNames
	rows          map[string]*reportRow
	active        map[string]time.Time
	phase         string
	phaseStarted  time.Time
	tracking      bool
	hidden        int
	showSkipped   bool
	color         bool
	rendered      int
	readyToRender bool
	aborted       bool
	stop          chan struct{}
	stopped       chan struct{}
	once          sync.Once
}

func newLiveReport(kinds []configurationKind, showSkipped bool) *liveReport {
	if !terminal(os.Stdout) && os.Getenv("NCR_LIVE") != "1" {
		return nil
	}
	report := &liveReport{
		kinds:       kinds,
		names:       make(groupedNames, len(kinds)),
		rows:        make(map[string]*reportRow),
		active:      make(map[string]time.Time),
		showSkipped: showSkipped,
		color:       colorEnabled(os.Stdout),
		stop:        make(chan struct{}),
		stopped:     make(chan struct{}),
	}
	go report.animate()
	return report
}

func (report *liveReport) animate() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer func() {
		ticker.Stop()
		close(report.stopped)
	}()
	for {
		select {
		case <-ticker.C:
			report.mu.Lock()
			if !report.aborted && (len(report.active) > 0 || report.phase != "" || report.tracking) {
				now := time.Now()
				for id, started := range report.active {
					report.rows[id].EvalTime = now.Sub(started)
				}
				report.renderLocked()
			}
			report.mu.Unlock()
		case <-report.stop:
			return
		}
	}
}

func (report *liveReport) discover(kind, name string) {
	report.mu.Lock()
	defer report.mu.Unlock()
	id := evaluationID(kind, name)
	if _, found := report.rows[id]; found {
		return
	}
	report.names[kind] = append(report.names[kind], name)
	report.rows[id] = &reportRow{
		Name:           name,
		System:         "--",
		EvalPending:    true,
		ClosurePending: true,
	}
}

func (report *liveReport) ready() {
	report.mu.Lock()
	defer report.mu.Unlock()
	for kind := range report.names {
		sort.Strings(report.names[kind])
	}
	report.readyToRender = true
	report.renderLocked()
}

func (report *liveReport) start(kind, name string) {
	report.mu.Lock()
	defer report.mu.Unlock()
	id := evaluationID(kind, name)
	if row := report.rows[id]; row != nil {
		row.EvalPending = false
		row.EvalTime = 0
		report.active[id] = time.Now()
		report.renderLocked()
	}
}

func (report *liveReport) selected(kind, name, system string) {
	report.mu.Lock()
	defer report.mu.Unlock()
	if row := report.rows[evaluationID(kind, name)]; row != nil {
		row.System = system
	}
}

func (report *liveReport) skipped(kind, name, system string) {
	report.mu.Lock()
	defer report.mu.Unlock()
	id := evaluationID(kind, name)
	if !report.showSkipped {
		delete(report.active, id)
		delete(report.rows, id)
		report.hidden++
		return
	}
	if row := report.rows[id]; row != nil {
		delete(report.active, id)
		row.System = system
		row.EvalPending = true
		row.Skipped = true
	}
}

func (report *liveReport) done(kind, name string, duration time.Duration) {
	report.mu.Lock()
	defer report.mu.Unlock()
	id := evaluationID(kind, name)
	if row := report.rows[id]; row != nil {
		delete(report.active, id)
		row.EvalPending = false
		row.EvalTime = duration
	}
}

func (report *liveReport) closure(kind, name string, stats closure) {
	if report == nil {
		return
	}
	report.mu.Lock()
	defer report.mu.Unlock()
	if row := report.rows[evaluationID(kind, name)]; row != nil {
		row.ClosureBytes = stats.Size
		row.Paths = stats.Paths
		row.ClosurePending = false
		if !report.tracking {
			report.renderLocked()
		}
	}
}

func (report *liveReport) beginClosure(kind, name string) {
	if report == nil {
		return
	}
	report.mu.Lock()
	defer report.mu.Unlock()
	if row := report.rows[evaluationID(kind, name)]; row != nil {
		row.ClosureBytes = 0
		row.Paths = 0
		row.ClosurePending = true
		report.tracking = true
		report.renderLocked()
	}
}

func (report *liveReport) endClosure() {
	if report == nil {
		return
	}
	report.mu.Lock()
	defer report.mu.Unlock()
	report.tracking = false
	report.renderLocked()
}

func (report *liveReport) setPhase(phase string) {
	if report == nil {
		return
	}
	report.mu.Lock()
	defer report.mu.Unlock()
	report.phase = phase
	if phase != "" {
		report.phaseStarted = time.Now()
	}
	report.renderLocked()
}

func (report *liveReport) abort() {
	if report == nil {
		return
	}
	report.mu.Lock()
	defer report.mu.Unlock()
	report.clearLocked()
	report.aborted = true
}

func (report *liveReport) finish(clear bool) {
	if report == nil {
		return
	}
	report.once.Do(func() {
		close(report.stop)
		<-report.stopped
		report.mu.Lock()
		defer report.mu.Unlock()
		if clear {
			report.clearLocked()
		}
		report.aborted = true
	})
}

func (report *liveReport) renderLocked() {
	if !report.readyToRender || report.aborted {
		return
	}
	sections := make([]reportSection, 0, len(report.kinds))
	for _, kind := range report.kinds {
		rows := make([]reportRow, 0, len(report.names[kind.Key]))
		for _, name := range report.names[kind.Key] {
			if row := report.rows[evaluationID(kind.Key, name)]; row != nil {
				rows = append(rows, *row)
			}
		}
		if len(rows) > 0 {
			sections = append(sections, reportSection{Kind: kind, Rows: rows})
		}
	}
	var output bytes.Buffer
	if report.rendered > 0 {
		_, _ = fmt.Fprintf(&output, "\x1b[%dA\r\x1b[J", report.rendered)
	}
	appendReports(&output, sections, report.hidden, report.color)
	if report.phase != "" {
		_, _ = fmt.Fprintf(
			&output,
			"\n%s %s\n",
			report.phase,
			paint(report.color, "96", formatDuration(time.Since(report.phaseStarted))),
		)
	}
	text := output.String()
	_, _ = os.Stdout.WriteString(text)
	report.rendered = strings.Count(text, "\n")
}

func (report *liveReport) clearLocked() {
	if report.rendered > 0 {
		_, _ = fmt.Fprintf(os.Stdout, "\x1b[%dA\r\x1b[J", report.rendered)
		report.rendered = 0
	}
}

func selectConfigurations(
	kinds []configurationKind,
	configs configurationSet,
	system string,
) (selected, skipped groupedNames, selectedCount, skippedCount int) {
	selected = make(groupedNames, len(kinds))
	skipped = make(groupedNames, len(kinds))
	for _, kind := range kinds {
		for name, config := range configs[kind.Key] {
			if system != "" && config.System != system {
				skipped[kind.Key] = append(skipped[kind.Key], name)
				skippedCount++
			} else {
				selected[kind.Key] = append(selected[kind.Key], name)
				selectedCount++
			}
		}
		sort.Strings(selected[kind.Key])
		sort.Strings(skipped[kind.Key])
	}
	return
}

func buildReports(
	kinds []configurationKind,
	selected, skipped groupedNames,
	result evaluation,
	progress *liveReport,
) ([]reportSection, error) {
	reports := make([]reportSection, 0, len(kinds))
	closures := make(map[string]closure)
	for _, kind := range kinds {
		names := selected[kind.Key]
		skippedNames := skipped[kind.Key]
		if len(names)+len(skippedNames) == 0 {
			continue
		}
		rows := make([]reportRow, 0, len(names)+len(skippedNames))
		for _, name := range names {
			config := result.Configurations[kind.Key][name]
			stats, ok := closures[config.Path]
			if !ok {
				var err error
				stats, err = closureStats(config.Path, kind.Key, name, progress)
				if err != nil {
					return nil, err
				}
				closures[config.Path] = stats
			} else {
				progress.closure(kind.Key, name, stats)
			}
			evalTime := result.EvalTimes[evaluationID(kind.Key, name)]
			rows = append(rows, reportRow{
				Name:         name,
				System:       config.System,
				ClosureBytes: stats.Size,
				Paths:        stats.Paths,
				EvalTime:     evalTime,
			})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].ClosureBytes == rows[j].ClosureBytes {
				return rows[i].Name < rows[j].Name
			}
			return rows[i].ClosureBytes > rows[j].ClosureBytes
		})
		for _, name := range skippedNames {
			rows = append(rows, reportRow{
				Name:           name,
				System:         result.Configurations[kind.Key][name].System,
				Skipped:        true,
				EvalPending:    true,
				ClosurePending: true,
			})
		}
		reports = append(reports, reportSection{Kind: kind, Rows: rows})
	}
	return reports, nil
}

func printReports(reports []reportSection, hidden int) error {
	color := colorEnabled(os.Stdout)
	var output bytes.Buffer
	appendReports(&output, reports, hidden, color)
	_, err := os.Stdout.WriteString(output.String())
	return err
}

func appendReports(output *bytes.Buffer, reports []reportSection, hidden int, color bool) {
	if len(reports) > 0 {
		printTable(output, reports, color)
	}
	printHidden(output, hidden, color)
}

func printHidden(output *bytes.Buffer, count int, color bool) {
	if count == 0 {
		return
	}
	configuration := "configurations"
	if count == 1 {
		configuration = "configuration"
	}
	message := fmt.Sprintf(
		"%d other-system %s hidden",
		count,
		configuration,
	)
	_, _ = fmt.Fprintln(output, paint(color, "2;90", message))
}

func printTable(output *bytes.Buffer, reports []reportSection, color bool) {
	showType := len(reports) > 1
	rowCount := 0
	for _, report := range reports {
		rowCount += len(report.Rows)
	}
	rows := make([]reportRow, 0, rowCount)
	var types []string
	if showType {
		types = make([]string, 0, rowCount)
	}
	for _, report := range reports {
		rows = append(rows, report.Rows...)
		if showType {
			for range report.Rows {
				types = append(types, report.Kind.Label)
			}
		}
	}
	systemColumn := 1
	if showType {
		systemColumn++
	}
	evalColumn := systemColumn + 1
	closureColumn := evalColumn + 1

	table := make([][]string, len(rows)+1)
	table[0] = make([]string, closureColumn+2)
	table[0][0] = "host"
	if showType {
		table[0][1] = "type"
	}
	table[0][systemColumn] = "system"
	table[0][evalColumn] = "eval"
	table[0][closureColumn] = "closure"
	table[0][closureColumn+1] = "paths"
	for i, row := range rows {
		eval, closureSize, paths := formatDuration(row.EvalTime), formatBytes(row.ClosureBytes), strconv.Itoa(row.Paths)
		if row.EvalPending {
			eval = "--"
		}
		if row.ClosurePending {
			closureSize, paths = "--", "--"
		}
		values := make([]string, len(table[0]))
		values[0] = row.Name
		if showType {
			values[1] = types[i]
		}
		values[systemColumn] = row.System
		values[evalColumn] = eval
		values[closureColumn] = closureSize
		values[closureColumn+1] = paths
		table[i+1] = values
	}

	widths := make([]int, len(table[0]))
	for _, row := range table {
		for column, value := range row {
			widths[column] = max(widths[column], utf8.RuneCountInString(value))
		}
	}
	for _, row := range rows {
		if row.ClosurePending && !row.Skipped {
			widths[systemColumn] = max(widths[systemColumn], len("aarch64-darwin"))
			widths[evalColumn] = max(widths[evalColumn], len("10m0.0s"))
			widths[closureColumn] = max(widths[closureColumn], len("499.2 MiB"))
			break
		}
	}
	styles := []string{"1", "94", "96", "92", "93"}
	if showType {
		styles = []string{"1", "95", "94", "96", "92", "93"}
	}
	vertical := paint(color, "90", "│")

	border := func(left, middle, right string) {
		var line strings.Builder
		line.WriteString(left)
		for column, width := range widths {
			if column > 0 {
				line.WriteString(middle)
			}
			line.WriteString(strings.Repeat("─", width+2))
		}
		line.WriteString(right)
		_, _ = fmt.Fprintln(output, paint(color, "90", line.String()))
	}
	printRow := func(row []string, data *reportRow) {
		_, _ = fmt.Fprint(output, vertical)
		for column, value := range row {
			padding := widths[column] - utf8.RuneCountInString(value)
			placeholder := data != nil && value == "--"
			style := "1;36"
			if data != nil {
				style = styles[column]
				if data.Skipped || data.System == "--" && column == systemColumn || data.EvalPending && column == evalColumn || data.ClosurePending && column >= closureColumn {
					style = "2;90"
				}
			}
			value = paint(color, style, value)
			if placeholder || column >= evalColumn {
				left := padding / 2
				_, _ = fmt.Fprintf(
					output,
					" %s%s%s %s",
					strings.Repeat(" ", left),
					value,
					strings.Repeat(" ", padding-left),
					vertical,
				)
			} else {
				_, _ = fmt.Fprintf(output, " %s%s %s", value, strings.Repeat(" ", padding), vertical)
			}
		}
		_, _ = fmt.Fprintln(output)
	}

	border("╭", "┬", "╮")
	printRow(table[0], nil)
	border("├", "┼", "┤")
	for index, row := range table[1:] {
		printRow(row, &rows[index])
	}
	border("╰", "┴", "╯")
}

func colorEnabled(file *os.File) bool {
	if value, present := os.LookupEnv("NO_COLOR"); present && value != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if colorForced() {
		return true
	}
	return terminal(file)
}

func colorForced() bool {
	value := os.Getenv("CLICOLOR_FORCE")
	return value != "" && value != "0"
}

func terminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func paint(enabled bool, code, value string) string {
	if !enabled {
		return value
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

func formatBytes(size int64) string {
	const unit = int64(1024)
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	value := float64(size)
	index := -1
	for value >= 1024 && index+1 < len(units) {
		value /= 1024
		index++
	}
	return fmt.Sprintf("%.1f %s", value, units[index])
}

func formatDuration(duration time.Duration) string {
	duration = duration.Round(100 * time.Millisecond)
	if duration < time.Minute {
		return fmt.Sprintf("%.1fs", duration.Seconds())
	}
	return fmt.Sprintf(
		"%dm%.1fs",
		duration/time.Minute,
		(duration % time.Minute).Seconds(),
	)
}
