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
)

const warmProjection = `
let
  flake = builtins.getFlake %s;
  requested = builtins.fromJSON %s;
  enabled = builtins.fromJSON %s;
  currentSystem = %s;
  sources = {
    nixos = flake.nixosConfigurations or {};
    darwin = flake.darwinConfigurations or {};
    home = flake.homeConfigurations or {};
  };
  names = kind:
    let configs = sources.${kind};
    in if requested == []
      then builtins.attrNames configs
      else builtins.filter (name: builtins.hasAttr name configs) requested;
  project = kind: name:
    let
      configs = sources.${kind};
      config = configs.${name};
      build =
        if kind == "home" then config.activationPackage
        else if kind == "darwin" then config.system
        else config.config.system.build.toplevel;
      filterSystem =
        if currentSystem == ""
        then build.system
        else config.pkgs.stdenv.buildPlatform.system or build.system;
    in
      if currentSystem != "" && filterSystem != currentSystem
      then filterSystem
      else builtins.deepSeq {
        system = build.system;
        path = build.outPath;
        drv = build.drvPath;
      } true;
in builtins.deepSeq (map (kind: map (project kind) (names kind)) enabled) true
`

const discoveryProjection = `
let
  flake = builtins.getFlake %s;
  enabled = builtins.fromJSON %s;
  sources = {
    nixos = flake.nixosConfigurations or {};
    darwin = flake.darwinConfigurations or {};
    home = flake.homeConfigurations or {};
  };
  present = {
    nixos = flake ? nixosConfigurations;
    darwin = flake ? darwinConfigurations;
    home = flake ? homeConfigurations;
  };
  forEnabled = value: builtins.listToAttrs (map (kind: {
    name = kind;
    value = value kind;
  }) enabled);
in {
  available = forEnabled (kind: builtins.attrNames sources.${kind});
  present = builtins.any (kind: present.${kind}) enabled;
}
`

const configurationProjection = `
let
  flake = builtins.getFlake %s;
  kind = %s;
  name = %s;
  currentSystem = %s;
  configs =
    if kind == "home" then flake.homeConfigurations
    else if kind == "darwin" then flake.darwinConfigurations
    else flake.nixosConfigurations;
  config = configs.${name};
  build =
    if kind == "home" then config.activationPackage
    else if kind == "darwin" then config.system
    else config.config.system.build.toplevel;
  filterSystem =
    if currentSystem == ""
    then build.system
    else config.pkgs.stdenv.buildPlatform.system or build.system;
in
  if currentSystem != "" && filterSystem != currentSystem
  then { system = filterSystem; path = ""; drv = ""; skipped = true; }
  else
    let result = {
      system = build.system;
      path = build.outPath;
      drv = build.drvPath;
      skipped = false;
    };
    in builtins.deepSeq result result
`

type configuration struct {
	System  string `json:"system"`
	Path    string `json:"path"`
	Drv     string `json:"drv"`
	Skipped bool   `json:"skipped"`
}

type configurationSet map[string]map[string]configuration

type evaluation struct {
	Configurations configurationSet
	Available      groupedNames
	Present        bool
	EvalTimes      map[string]time.Duration
}

type discovery struct {
	Available groupedNames `json:"available"`
	Present   bool         `json:"present"`
}

type closure struct {
	Size  int64
	Paths int
}

func warm(opts options, enabled []configurationKind, system string) error {
	flake, err := flakeURL(opts.flake)
	if err != nil {
		return err
	}
	requested, _ := json.Marshal(opts.names)
	keys := make([]string, len(enabled))
	for i, kind := range enabled {
		keys[i] = kind.Key
	}
	kinds, _ := json.Marshal(keys)
	var result bool
	return nixEval(fmt.Sprintf(
		warmProjection,
		nixString(flake),
		nixString(string(requested)),
		nixString(string(kinds)),
		nixString(system),
	), &result)
}

func evaluate(
	opts options,
	enabled []configurationKind,
	system string,
	progress *liveReport,
) (evaluation, error) {
	flake, err := flakeURL(opts.flake)
	if err != nil {
		return evaluation{}, err
	}
	keys := make([]string, len(enabled))
	for i, kind := range enabled {
		keys[i] = kind.Key
	}
	kinds, _ := json.Marshal(keys)
	var found discovery
	if err := nixEval(fmt.Sprintf(
		discoveryProjection,
		nixString(flake),
		nixString(string(kinds)),
	), &found); err != nil {
		return evaluation{}, err
	}

	result := evaluation{
		Configurations: make(configurationSet, len(enabled)),
		Available:      found.Available,
		Present:        found.Present,
		EvalTimes:      make(map[string]time.Duration),
	}
	selected := make(groupedNames, len(enabled))
	for _, kind := range enabled {
		result.Configurations[kind.Key] = make(map[string]configuration)
		selected[kind.Key] = selectNames(opts.names, result.Available[kind.Key])
		if progress != nil {
			for _, name := range selected[kind.Key] {
				progress.discover(kind.Key, name)
			}
		}
	}
	if progress != nil {
		progress.ready()
	}

	for _, kind := range enabled {
		for _, name := range selected[kind.Key] {
			expression := fmt.Sprintf(
				configurationProjection,
				nixString(flake),
				nixString(kind.Key),
				nixString(name),
				nixString(system),
			)
			if progress != nil {
				progress.start(kind.Key, name)
			}
			started := time.Now()
			var config configuration
			err := nixEval(expression, &config)
			duration := time.Since(started)
			if err != nil {
				if progress != nil {
					progress.abort()
				}
				return evaluation{}, fmt.Errorf("evaluate %s configuration %q: %w", kind.Label, name, err)
			}
			result.Configurations[kind.Key][name] = config
			if config.Skipped {
				if progress != nil {
					progress.skipped(kind.Key, name, config.System)
				}
				continue
			}
			result.EvalTimes[evaluationID(kind.Key, name)] = duration
			if progress != nil {
				progress.done(kind.Key, name, config.System, duration)
			}
		}
	}
	return result, nil
}

func selectNames(requested, available []string) []string {
	if len(requested) == 0 {
		return available
	}
	found := make(map[string]struct{}, len(available))
	for _, name := range available {
		found[name] = struct{}{}
	}
	selected := make([]string, 0, len(requested))
	for _, name := range requested {
		if _, ok := found[name]; ok {
			selected = append(selected, name)
			delete(found, name)
		}
	}
	return selected
}

func flakeURL(flake string) (string, error) {
	output, err := nixOutput(
		[]string{"flake", "metadata", "--quiet", "--json", flake},
	)
	if err != nil {
		return "", err
	}
	var metadata struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(output, &metadata); err != nil {
		return "", fmt.Errorf("decode flake metadata: %w", err)
	}
	if metadata.URL == "" {
		return "", fmt.Errorf("nix reported an empty flake URL")
	}
	return metadata.URL, nil
}

func realise(kinds []configurationKind, selected groupedNames, result evaluation, progress *liveReport) error {
	derivations := make(map[string]struct{})
	for _, kind := range kinds {
		for _, name := range selected[kind.Key] {
			id := evaluationID(kind.Key, name)
			if _, ok := result.EvalTimes[id]; !ok {
				return fmt.Errorf(
					"missing evaluation timing for %s configuration %q",
					kind.Label,
					name,
				)
			}
			derivations[result.Configurations[kind.Key][name].Drv] = struct{}{}
		}
	}
	args := []string{"--realise", "--quiet", "--no-build-output"}
	for drv := range derivations {
		args = append(args, drv)
	}
	sort.Strings(args[3:])
	cmd := exec.Command("nix-store", args...)
	var diagnostics bytes.Buffer
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.Discard
	cmd.Stderr = &diagnostics
	if err := cmd.Run(); err != nil {
		progress.abort()
		_, _ = diagnostics.WriteTo(os.Stderr)
		return fmt.Errorf("realise selected configuration closures: %w", err)
	}
	return nil
}

func closureStats(path, kind, name string, progress *liveReport) (closure, error) {
	progress.beginClosure(kind, name)
	defer progress.endClosure()

	cmd := exec.Command("nix", "path-info", "--recursive", "--size", path)
	cmd.Env = append(os.Environ(), "CLICOLOR_FORCE=0", "NO_COLOR=1")
	cmd.Stdin = os.Stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return closure{}, fmt.Errorf("capture nix path-info output: %w", err)
	}
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		return closure{}, fmt.Errorf("start nix path-info: %w", err)
	}

	var stats closure
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	var parseErr error
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		separator := strings.LastIndexAny(line, " \t")
		if separator < 1 {
			if parseErr == nil {
				parseErr = fmt.Errorf("invalid nix path-info line %q", line)
			}
			continue
		}
		pathSize, err := strconv.ParseInt(line[separator+1:], 10, 64)
		if err != nil {
			if parseErr == nil {
				parseErr = fmt.Errorf("invalid nix path-info size: %w", err)
			}
			continue
		}
		stats.Size += pathSize
		stats.Paths++
		progress.closure(kind, name, stats)
	}
	scanErr := scanner.Err()
	runErr := cmd.Wait()
	if diagnostics.Len() > 0 {
		progress.abort()
		_, _ = diagnostics.WriteTo(os.Stderr)
	}
	if scanErr != nil {
		return closure{}, fmt.Errorf("read nix path-info output: %w", scanErr)
	}
	if parseErr != nil {
		return closure{}, parseErr
	}
	if runErr != nil {
		return closure{}, fmt.Errorf("nix path-info failed: %w", runErr)
	}
	return stats, nil
}

func nixSystem() (string, error) {
	output, err := nixOutput([]string{"config", "show", "system"})
	if err != nil {
		return "", err
	}
	system := strings.TrimSpace(string(output))
	if system == "" {
		return "", fmt.Errorf("nix reported an empty system")
	}
	return system, nil
}

func nixEval(expression string, value any) error {
	output, err := nixOutput(
		[]string{"eval", "--impure", "--quiet", "--json", "--expr", expression},
	)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(output, value); err != nil {
		return fmt.Errorf("decode nix output: %w", err)
	}
	return nil
}

func nixOutput(args []string) ([]byte, error) {
	cmd := exec.Command("nix", args...)
	cmd.Env = append(os.Environ(), "CLICOLOR_FORCE=0", "NO_COLOR=1")
	operation := args[0]
	if operation == "flake" && len(args) > 1 {
		operation += " " + args[1]
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("nix %s failed: %w", operation, err)
	}
	return output.Bytes(), nil
}

func evaluationID(kind, name string) string {
	return kind + "\x00" + name
}

func nixString(value string) string {
	return `"` + strings.NewReplacer(
		`\`, `\\`, `"`, `\"`, `${`, `\${`,
	).Replace(value) + `"`
}
