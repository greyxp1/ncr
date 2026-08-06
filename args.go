package main

import (
	"fmt"
	"os"
	"strings"
)

type options struct {
	flake       string
	names       []string
	kind        string
	allSystems  bool
	showSkipped bool
	warmOnly    bool
	showHelp    bool
	showVersion bool
}

const usageHint = "Run 'ncr --help' for usage."

func parseArgs(args []string) (options, error) {
	opts := options{names: []string{}}

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			opts.showHelp = true
		case "-v", "--version":
			opts.showVersion = true
		}
	}
	if opts.showHelp || opts.showVersion {
		return opts, nil
	}

flags:
	for len(args) > 0 {
		switch args[0] {
		case "--home":
			opts.kind = "home"
		case "--all-systems":
			opts.allSystems = true
		case "--show-skipped":
			opts.showSkipped = true
		case "--warm-only":
			opts.warmOnly = true
		default:
			if strings.HasPrefix(args[0], "--") {
				return options{}, fmt.Errorf("unknown option %q; %s", args[0], usageHint)
			}
			break flags
		}
		args = args[1:]
	}
	var fragment string
	switch {
	case len(args) == 0:
	case isConfigurationSelector(args[0]):
		fragment = args[0]
		opts.names = args[1:]
	case isConfigurationName(args[0]):
		opts.names = args
	default:
		opts.flake, fragment = splitInstallable(args[0])
		opts.names = args[1:]
	}
	if opts.flake == "" {
		opts.flake = os.Getenv("NCR_FLAKE")
		if opts.flake == "" {
			return options{}, fmt.Errorf("missing flake reference and NCR_FLAKE is not set")
		}
	}
	if kind, name, qualified := splitConfiguration(fragment); qualified {
		if opts.kind != "" && opts.kind != kind {
			return options{}, fmt.Errorf("--home conflicts with %q", fragment)
		}
		opts.kind = kind
		if name != "" {
			opts.names = append([]string{name}, opts.names...)
		}
	} else if fragment != "" {
		opts.names = append([]string{fragment}, opts.names...)
	}
	for _, name := range opts.names {
		if strings.HasPrefix(name, "--") {
			return options{}, fmt.Errorf("unknown option %q; %s", name, usageHint)
		}
	}
	return opts, nil
}

func isConfigurationName(value string) bool {
	return value != "." && value != ".." && !strings.ContainsAny(value, "/:#")
}

func isConfigurationSelector(value string) bool {
	_, _, qualified := splitConfiguration(value)
	return qualified
}

func splitInstallable(installable string) (flake, fragment string) {
	flake, fragment, found := strings.Cut(installable, "#")
	if !found {
		return installable, ""
	}
	if flake == "" {
		flake = "."
	}
	return
}

func splitConfiguration(fragment string) (kind, name string, qualified bool) {
	for _, candidate := range configurationKinds {
		if fragment == candidate.Output {
			return candidate.Key, "", true
		}
		if name, found := strings.CutPrefix(fragment, candidate.Output+"."); found {
			return candidate.Key, name, true
		}
	}
	return "", "", false
}
