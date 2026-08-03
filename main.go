package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type configurationKind struct {
	Key    string
	Output string
	Label  string
}

var configurationKinds = [...]configurationKind{
	{Key: "nixos", Output: "nixosConfigurations", Label: "NixOS"},
	{Key: "darwin", Output: "darwinConfigurations", Label: "nix-darwin"},
	{Key: "home", Output: "homeConfigurations", Label: "Home Manager"},
}

type groupedNames map[string][]string

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err == nil {
		err = run(opts)
	}
	if err != nil {
		exitError(err)
	}
}

func run(opts options) error {
	system := ""
	var err error
	if len(opts.names) == 0 && !opts.allSystems {
		system, err = nixSystem()
		if err != nil {
			return err
		}
	}

	enabled := enabledKinds(opts.kind)
	if opts.warmOnly {
		printWarmup()
		if err := warm(opts, enabled, system); err != nil {
			return fmt.Errorf("warm evaluation: %w", err)
		}
		return nil
	}
	live := newLiveReport(enabled, opts.showSkipped)
	defer live.finish(false)
	result, err := evaluate(opts, enabled, system, live)
	if err != nil {
		return err
	}

	if !result.Present {
		if len(enabled) == 1 {
			return fmt.Errorf(
				"flake %q does not provide %s",
				opts.flake,
				enabled[0].Output,
			)
		}
		return fmt.Errorf(
			"flake %q provides none of nixosConfigurations, darwinConfigurations, or homeConfigurations",
			opts.flake,
		)
	}

	if missing := missingNames(opts.names, result.Available); len(missing) > 0 {
		for i, name := range missing {
			missing[i] = strconv.Quote(name)
		}
		available := formatGrouped(enabled, result.Available)
		if available == "" {
			available = "none"
		}
		suffix := ""
		if len(missing) > 1 {
			suffix = "s"
		}
		return fmt.Errorf(
			"unknown configuration%s %s; available: %s",
			suffix,
			strings.Join(missing, ", "),
			available,
		)
	}

	selected, skipped, selectedCount, skippedCount := selectConfigurations(
		enabled,
		result.Configurations,
		system,
	)
	if selectedCount == 0 {
		live.abort()
		if skippedCount > 0 {
			return fmt.Errorf(
				"no supported configurations match system %q; available: %s",
				system,
				formatSkipped(enabled, skipped, result.Configurations),
			)
		}
		return fmt.Errorf(
			"no supported configurations found in %q",
			opts.flake,
		)
	}

	visibleSkipped := skipped
	hiddenCount := 0
	if !opts.showSkipped {
		visibleSkipped = nil
		hiddenCount = skippedCount
	}

	live.beginBuilding()
	if err := realise(enabled, selected, result, live); err != nil {
		return err
	}
	reports, err := buildReports(enabled, selected, visibleSkipped, result, live)
	if err != nil {
		return err
	}
	live.finish(true)
	return printReports(reports, hiddenCount)
}

func formatSkipped(kinds []configurationKind, groups groupedNames, configs configurationSet) string {
	formatted := make(groupedNames, len(groups))
	for _, kind := range kinds {
		for _, name := range groups[kind.Key] {
			formatted[kind.Key] = append(
				formatted[kind.Key],
				fmt.Sprintf("%s (%s)", name, configs[kind.Key][name].System),
			)
		}
	}
	return formatGrouped(kinds, formatted)
}

func enabledKinds(only string) []configurationKind {
	if only == "" {
		return configurationKinds[:]
	}
	for _, kind := range configurationKinds {
		if kind.Key == only {
			return []configurationKind{kind}
		}
	}
	return nil
}

func missingNames(requested []string, available groupedNames) []string {
	if len(requested) == 0 {
		return nil
	}
	missing := make(map[string]struct{}, len(requested))
	for _, name := range requested {
		missing[name] = struct{}{}
	}
	for _, names := range available {
		for _, name := range names {
			delete(missing, name)
		}
	}
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func formatGrouped(kinds []configurationKind, groups groupedNames) string {
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if len(groups[kind.Key]) > 0 {
			parts = append(
				parts,
				fmt.Sprintf("%s: %s", kind.Label, strings.Join(groups[kind.Key], ", ")),
			)
		}
	}
	return strings.Join(parts, "; ")
}

func exitError(err error) {
	fmt.Fprintln(os.Stderr, "ncr:", err)
	os.Exit(1)
}
