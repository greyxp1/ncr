package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const projection = `
configs:
let
  requested = builtins.fromJSON %s;
  selected =
    if requested == []
    then configs
    else builtins.listToAttrs (map (name: {
      inherit name;
      value = configs.${name} or (throw "unknown NixOS configuration '${name}'; available: ${builtins.concatStringsSep ", " (builtins.attrNames configs)}");
    }) requested);
in builtins.mapAttrs (name: config:
  let
    toplevel = config.config.system.build.toplevel;
    result = {
      system = config.pkgs.stdenv.hostPlatform.system;
      path = toplevel.outPath;
      drv = toplevel.drvPath;
    };
  in builtins.trace "ncr-eval-start:${name}"
    (builtins.deepSeq result (builtins.trace "ncr-eval-done:${name}" result))
) selected
`

type options struct {
	flake string
	hosts []string
}

type configuration struct {
	System string `json:"system"`
	Path   string `json:"path"`
	Drv    string `json:"drv"`
}

type reportRow struct {
	Host         string
	System       string
	ClosureBytes int64
	Paths        int
	EvalTime     time.Duration
	Deduped      bool
}

type pathInfo struct {
	Path    string `json:"path"`
	NARSize int64  `json:"narSize"`
}

func main() {
	if err := run(parseArgs(os.Args[1:])); err != nil {
		exitError(err)
	}
}

func parseArgs(args []string) options {
	opts := options{flake: ".", hosts: []string{}}
	if len(args) > 0 {
		var host string
		opts.flake, host = splitInstallable(args[0])
		opts.hosts = args[1:]
		if host != "" {
			opts.hosts = append([]string{host}, opts.hosts...)
		}
	}
	return opts
}

func splitInstallable(installable string) (flake, host string) {
	flake, host, found := strings.Cut(installable, "#")
	if !found {
		return installable, ""
	}
	if flake == "" {
		flake = "."
	}
	if host == "nixosConfigurations" {
		host = ""
	} else {
		host = strings.TrimPrefix(host, "nixosConfigurations.")
	}
	return
}

func run(opts options) error {
	progress := terminal(os.Stderr)
	requested, _ := json.Marshal(opts.hosts)
	configs := make(map[string]configuration)
	evalArgs := []string{
		"eval",
		"--quiet",
		"--json",
		"--apply",
		fmt.Sprintf(projection, nixString(string(requested))),
		opts.flake + "#nixosConfigurations",
	}
	evalTimes, err := nixEvalJSON(evalArgs, &configs, progress)
	if err != nil {
		return err
	}

	selected := make([]string, 0, len(configs))
	for host := range configs {
		selected = append(selected, host)
	}
	sort.Strings(selected)
	if len(selected) == 0 {
		return fmt.Errorf("no NixOS configurations found in %q", opts.flake)
	}

	args := []string{"--realise", "--quiet", "--no-build-output"}
	for _, host := range selected {
		if _, ok := evalTimes[host]; !ok {
			return fmt.Errorf("missing evaluation timing for %q", host)
		}
		args = append(args, configs[host].Drv)
	}
	cmd := exec.Command("nix-store", args...)
	var diagnostics bytes.Buffer
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.Discard
	cmd.Stderr = &diagnostics
	if err := cmd.Run(); err != nil {
		_, _ = diagnostics.WriteTo(os.Stderr)
		return fmt.Errorf("realise selected host closures: %w", err)
	}

	rows := make([]reportRow, 0, len(selected)+1)
	allPaths := make(map[string]int64)
	var totalEvalTime time.Duration
	for _, host := range selected {
		config := configs[host]
		evalTime := evalTimes[host]
		size, paths, err := closureStats(config.Path, allPaths)
		if err != nil {
			return err
		}
		totalEvalTime += evalTime
		rows = append(rows, reportRow{
			Host:         host,
			System:       config.System,
			ClosureBytes: size,
			Paths:        paths,
			EvalTime:     evalTime,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ClosureBytes == rows[j].ClosureBytes {
			return rows[i].Host < rows[j].Host
		}
		return rows[i].ClosureBytes > rows[j].ClosureBytes
	})

	var size int64
	for _, pathSize := range allPaths {
		size += pathSize
	}
	rows = append(rows, reportRow{
		Host:         "all hosts",
		ClosureBytes: size,
		Paths:        len(allPaths),
		EvalTime:     totalEvalTime,
		Deduped:      true,
	})

	printTable(rows)
	return nil
}

func closureStats(path string, allPaths map[string]int64) (int64, int, error) {
	output, err := nixOutput([]string{
		"path-info", "--json", "--recursive", "--size", path,
	}, printNixLine)
	if err != nil {
		return 0, 0, err
	}

	var entries []pathInfo
	if err := json.Unmarshal(output, &entries); err != nil {
		return 0, 0, fmt.Errorf("decode nix path-info output: %w", err)
	}

	var size int64
	for _, entry := range entries {
		size += entry.NARSize
		allPaths[entry.Path] = entry.NARSize
	}
	return size, len(entries), nil
}

func nixEvalJSON(args []string, value any, progress bool) (map[string]time.Duration, error) {
	started := make(map[string]time.Time)
	durations := make(map[string]time.Duration)
	handleLine := func(line string) error {
		now := time.Now()
		if host, found := strings.CutPrefix(line, "trace: ncr-eval-start:"); found {
			started[host] = now
			if progress {
				color := colorEnabled(os.Stderr)
				_, err := fmt.Fprintf(
					os.Stderr,
					"%s Evaluating %s\n",
					paint(color, "36", "→"),
					paint(color, "1", host),
				)
				return err
			}
			return nil
		}
		if host, found := strings.CutPrefix(line, "trace: ncr-eval-done:"); found {
			if start, ok := started[host]; ok {
				durations[host] = now.Sub(start)
				delete(started, host)
			}
			return nil
		}
		return printNixLine(line)
	}
	output, err := nixOutput(args, handleLine)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(output, value); err != nil {
		return nil, fmt.Errorf("decode nix output: %w", err)
	}
	return durations, nil
}

func nixOutput(args []string, handleLine func(string) error) ([]byte, error) {
	cmd := exec.Command("nix", args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("capture nix %s diagnostics: %w", args[0], err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start nix %s: %w", args[0], err)
	}

	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var writeErr error
	for scanner.Scan() {
		if err := handleLine(scanner.Text()); err != nil && writeErr == nil {
			writeErr = err
		}
	}
	scanErr := scanner.Err()
	runErr := cmd.Wait()
	if scanErr != nil {
		return nil, fmt.Errorf("read nix %s diagnostics: %w", args[0], scanErr)
	}
	if writeErr != nil {
		return nil, fmt.Errorf("write nix diagnostics: %w", writeErr)
	}
	if runErr != nil {
		return nil, fmt.Errorf("nix %s failed: %w", args[0], runErr)
	}
	return output.Bytes(), nil
}

func printNixLine(line string) error {
	_, err := fmt.Fprintln(os.Stderr, line)
	return err
}

func printTable(rows []reportRow) {
	color := colorEnabled(os.Stdout)

	table := make([][]string, len(rows)+1)
	table[0] = []string{"host", "system", "eval", "closure", "paths"}
	for i, row := range rows {
		table[i+1] = []string{
			row.Host,
			row.System,
			formatDuration(row.EvalTime),
			formatBytes(row.ClosureBytes),
			strconv.Itoa(row.Paths),
		}
	}

	widths := make([]int, len(table[0]))
	for _, row := range table {
		for column, value := range row {
			widths[column] = max(widths[column], utf8.RuneCountInString(value))
		}
	}
	styles := [...]string{"1", "94", "96", "92", "93"}

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
		fmt.Println(paint(color, "90", line.String()))
	}
	printRow := func(row []string, numeric, total bool) {
		fmt.Print(paint(color, "90", "│"))
		for column, value := range row {
			padding := strings.Repeat(
				" ",
				widths[column]-utf8.RuneCountInString(value),
			)
			style := "1;36"
			if numeric {
				style = styles[column]
				if total && column == 0 {
					style = "1;95"
				}
			}
			value = paint(color, style, value)
			if numeric && column >= 2 {
				fmt.Printf(" %s%s %s", padding, value, paint(color, "90", "│"))
			} else {
				fmt.Printf(" %s%s %s", value, padding, paint(color, "90", "│"))
			}
		}
		fmt.Println()
	}

	border("╭", "┬", "╮")
	printRow(table[0], false, false)
	border("├", "┼", "┤")
	for index, row := range table[1:] {
		if index > 0 && rows[index].Deduped {
			border("├", "┼", "┤")
		}
		printRow(row, true, rows[index].Deduped)
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
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return true
	}
	return terminal(file)
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

func nixString(value string) string {
	return `"` + strings.NewReplacer(
		`\`, `\\`, `"`, `\"`, `${`, `\${`,
	).Replace(value) + `"`
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
	switch {
	case duration < time.Second:
		return duration.Round(time.Millisecond).String()
	case duration < time.Minute:
		return duration.Round(100 * time.Millisecond).String()
	default:
		return duration.Round(time.Second).String()
	}
}

func exitError(err error) {
	fmt.Fprintln(os.Stderr, "ncr:", err)
	os.Exit(1)
}
