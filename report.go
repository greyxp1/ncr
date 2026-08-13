package main

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
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

type livePhase uint8

const (
	phasePreparing livePhase = iota
	phaseEvaluating
	phaseBuilding
	phaseMeasuring
)

type liveReport struct {
	mu          sync.Mutex
	phase       livePhase
	name        string
	evalStarted time.Time
	color       bool
	rendered    bool
	aborted     bool
	stop        chan struct{}
	stopped     chan struct{}
	once        sync.Once
}

func newLiveReport() *liveReport {
	if !terminal(os.Stdout) && os.Getenv("NCR_LIVE") != "1" {
		return nil
	}
	report := &liveReport{
		color:   colorEnabled(os.Stdout),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go report.animate()
	return report
}

func printWarmup() {
	if !terminal(os.Stdout) {
		return
	}
	fmt.Fprintf(os.Stdout, "\n%s Warming evaluation\n", paint(colorEnabled(os.Stdout), "1;32", ">"))
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
			if !report.aborted && report.phase == phaseEvaluating {
				report.renderLocked()
			}
			report.mu.Unlock()
		case <-report.stop:
			return
		}
	}
}

func (report *liveReport) setPhase(phase livePhase, name string) {
	if report == nil {
		return
	}
	report.mu.Lock()
	defer report.mu.Unlock()
	report.phase = phase
	report.name = name
	if phase == phaseEvaluating {
		report.evalStarted = time.Now()
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

func (report *liveReport) finish() {
	if report == nil {
		return
	}
	report.once.Do(func() {
		close(report.stop)
		<-report.stopped
		report.mu.Lock()
		defer report.mu.Unlock()
		report.clearLocked()
		report.aborted = true
	})
}

func (report *liveReport) renderLocked() {
	if report.aborted {
		return
	}
	action := "Preparing"
	switch report.phase {
	case phaseEvaluating:
		action = "Evaluating"
	case phaseMeasuring:
		action = "Measuring"
	case phaseBuilding:
		action = "Building"
	}
	plain := "→ " + action
	status := paint(report.color, "36", "→") + " " + action
	if report.name != "" {
		plain += " " + report.name
		status += " " + paint(report.color, "1", report.name)
	}
	if report.phase == phaseEvaluating {
		duration := formatDuration(time.Since(report.evalStarted))
		plain += " " + duration
		status += " " + paint(report.color, "36", duration)
	}
	if terminal(os.Stdout) {
		clipped := truncateWidth(plain, max(terminalWidth(os.Stdout)-1, 0))
		if clipped != plain {
			status = paint(report.color, "36", clipped)
		}
	}
	_, _ = fmt.Fprintf(os.Stdout, "\r\x1b[2K%s", status)
	report.rendered = true
}

func truncateWidth(value string, remaining int) string {
	end := 0
	for offset, character := range value {
		width := 1
		if character > 127 {
			width = 2
		}
		if width > remaining {
			return value[:end]
		}
		remaining -= width
		end = offset + utf8.RuneLen(character)
	}
	return value
}

func (report *liveReport) clearLocked() {
	if report.rendered {
		_, _ = fmt.Fprint(os.Stdout, "\r\x1b[2K")
		report.rendered = false
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
	realised map[string]string,
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
			path := config.Path
			if resolved, ok := realised[config.Drv]; ok {
				path = resolved
			}
			if placeholderPath(path) {
				rows = append(rows, reportRow{
					Name:           name,
					System:         config.System,
					ClosureBytes:   0,
					Paths:          0,
					EvalTime:       result.EvalTimes[evaluationID(kind.Key, name)],
					ClosurePending: true,
				})
				continue
			}
			stats, ok := closures[path]
			if !ok {
				var err error
				stats, err = closureStats(name, path, progress)
				if err != nil {
					return nil, err
				}
				closures[path] = stats
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

func placeholderPath(path string) bool {
	base := path
	if slash := strings.LastIndex(base, "/"); slash >= 0 {
		base = base[slash+1:]
	}
	return base != "" && !strings.Contains(base, "-")
}

func printReports(reports []reportSection, hidden int) error {
	color := colorEnabled(os.Stdout)
	var output bytes.Buffer
	if len(reports) > 0 {
		printTable(&output, reports, color)
	}
	printHidden(&output, hidden, color)
	_, err := output.WriteTo(os.Stdout)
	return err
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
		eval, closureSize, paths := "", "", ""
		if row.Skipped {
			eval, closureSize, paths = "—", "—", "—"
		} else {
			if !row.EvalPending {
				eval = formatDuration(row.EvalTime)
			}
			if !row.ClosurePending {
				closureSize = formatBytes(row.ClosureBytes)
				paths = strconv.Itoa(row.Paths)
			}
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
	widths[systemColumn] = max(widths[systemColumn], len("x86_64-linux"))
	widths[evalColumn] = max(widths[evalColumn], len("20.5s"))
	widths[closureColumn] = max(widths[closureColumn], len("20.0 GiB"))
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
	pendingCell := func(row *reportRow, column int) bool {
		return row != nil && (column == systemColumn && row.System == "" ||
			column == evalColumn && row.EvalPending ||
			column >= closureColumn && row.ClosurePending)
	}
	printRow := func(row []string, data *reportRow) {
		_, _ = fmt.Fprint(output, vertical)
		for column, value := range row {
			padding := widths[column] - utf8.RuneCountInString(value)
			pending := pendingCell(data, column)
			style := "1;36"
			if data != nil {
				style = styles[column]
				if data.Skipped || pending {
					style = "2;90"
				}
			}
			value = paint(color, style, value)
			if data != nil && column >= evalColumn && !pending {
				_, _ = fmt.Fprintf(output, " %s%s %s", strings.Repeat(" ", padding), value, vertical)
			} else if pending || column >= evalColumn {
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
	if value := os.Getenv("CLICOLOR_FORCE"); value != "" && value != "0" {
		return true
	}
	return terminal(file)
}

func terminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func terminalWidth(file *os.File) int {
	if !terminal(file) {
		return 0
	}
	var size struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		file.Fd(),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&size)),
	)
	if errno != 0 {
		return 0
	}
	return int(size.Col)
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
