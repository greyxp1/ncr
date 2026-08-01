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

const projection = `
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
  present = {
    nixos = flake ? nixosConfigurations;
    darwin = flake ? darwinConfigurations;
    home = flake ? homeConfigurations;
  };
  forEnabled = value: builtins.listToAttrs (map (kind: {
    name = kind;
    value = value kind;
  }) enabled);
  names = forEnabled (kind:
    let configs = sources.${kind};
    in if requested == []
      then builtins.attrNames configs
      else builtins.filter (name: builtins.hasAttr name configs) requested);
  project = kind:
    let
      configs = sources.${kind};
    in builtins.listToAttrs (map (name: {
      inherit name;
      value =
        let
          config = configs.${name};
          build =
            if kind == "home"
            then config.activationPackage
            else if kind == "darwin"
            then config.system
            else config.config.system.build.toplevel;
          result = {
            system = build.system;
            path = build.outPath;
            drv = build.drvPath;
          };
        in builtins.trace "ncr-eval-start:${kind}:${name}"
          (builtins.seq result.system (
            if currentSystem != "" && result.system != currentSystem
            then builtins.trace "ncr-eval-skip:${kind}:${result.system}:${name}"
              { inherit (result) system; path = ""; drv = ""; }
            else builtins.trace "ncr-eval-selected:${kind}:${result.system}:${name}"
              (builtins.deepSeq result
                (builtins.trace "ncr-eval-done:${kind}:${name}" result))));
    }) names.${kind});
  discovery = builtins.toJSON names;
in builtins.trace "ncr-discovered:${discovery}" {
    configurations = forEnabled project;
    available =
      if requested == []
      then {}
      else forEnabled (kind: builtins.attrNames sources.${kind});
    present = builtins.any (kind: present.${kind}) enabled;
  }
`

type configuration struct {
	System string `json:"system"`
	Path   string `json:"path"`
	Drv    string `json:"drv"`
}

type configurationSet map[string]map[string]configuration

type evaluation struct {
	Configurations configurationSet         `json:"configurations"`
	Available      groupedNames             `json:"available"`
	Present        bool                     `json:"present"`
	EvalTimes      map[string]time.Duration `json:"-"`
}

type closure struct {
	Size  int64
	Paths int
}

func evaluate(opts options, enabled []configurationKind, system string, progress *liveReport) (evaluation, error) {
	flake, err := flakeURL(opts.flake)
	if err != nil {
		return evaluation{}, err
	}
	requested, _ := json.Marshal(opts.names)
	keys := make([]string, len(enabled))
	for i, kind := range enabled {
		keys[i] = kind.Key
	}
	kinds, _ := json.Marshal(keys)
	result := evaluation{}
	times, err := nixEvalJSON([]string{
		"eval",
		"--impure",
		"--quiet",
		"--json",
		"--expr",
		fmt.Sprintf(
			projection,
			nixString(flake),
			nixString(string(requested)),
			nixString(string(kinds)),
			nixString(system),
		),
	}, &result, progress)
	result.EvalTimes = times
	return result, err
}

func flakeURL(flake string) (string, error) {
	output, err := nixOutput(
		[]string{"flake", "metadata", "--quiet", "--json", flake},
		printNixLine,
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
	output, err := nixOutput([]string{"config", "show", "system"}, printNixLine)
	if err != nil {
		return "", err
	}
	system := strings.TrimSpace(string(output))
	if system == "" {
		return "", fmt.Errorf("nix reported an empty system")
	}
	return system, nil
}

func nixEvalJSON(args []string, value any, progress *liveReport) (map[string]time.Duration, error) {
	started := make(map[string]time.Time)
	durations := make(map[string]time.Duration)
	handleLine := func(line string) error {
		if value, found := strings.CutPrefix(line, "trace: ncr-discovered:"); found {
			var discovered groupedNames
			if err := json.Unmarshal([]byte(value), &discovered); err != nil {
				return printNixLine(line)
			}
			if progress != nil {
				for kind, names := range discovered {
					for _, name := range names {
						progress.discover(kind, name)
					}
				}
				progress.ready()
			}
			return nil
		}
		if value, found := strings.CutPrefix(line, "trace: ncr-eval-start:"); found {
			kind, name, valid := strings.Cut(value, ":")
			if !valid {
				return printNixLine(line)
			}
			started[evaluationID(kind, name)] = time.Now()
			if progress != nil {
				progress.start(kind, name)
			}
			return nil
		}
		if value, found := strings.CutPrefix(line, "trace: ncr-eval-selected:"); found {
			kind, value, valid := strings.Cut(value, ":")
			if !valid {
				return printNixLine(line)
			}
			system, name, valid := strings.Cut(value, ":")
			if !valid {
				return printNixLine(line)
			}
			if progress != nil {
				progress.selected(kind, name, system)
			}
			return nil
		}
		if value, found := strings.CutPrefix(line, "trace: ncr-eval-skip:"); found {
			kind, value, valid := strings.Cut(value, ":")
			if !valid {
				return printNixLine(line)
			}
			system, name, valid := strings.Cut(value, ":")
			if !valid {
				return printNixLine(line)
			}
			delete(started, evaluationID(kind, name))
			if progress != nil {
				progress.skipped(kind, name, system)
			}
			return nil
		}
		if value, found := strings.CutPrefix(line, "trace: ncr-eval-done:"); found {
			kind, name, valid := strings.Cut(value, ":")
			if !valid {
				return printNixLine(line)
			}
			id := evaluationID(kind, name)
			if start, ok := started[id]; ok {
				durations[id] = time.Since(start)
				delete(started, id)
				if progress != nil {
					progress.done(kind, name, durations[id])
				}
			}
			return nil
		}
		if progress != nil {
			progress.abort()
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
	cmd.Env = append(os.Environ(), "CLICOLOR_FORCE=0", "NO_COLOR=1")
	operation := args[0]
	if operation == "flake" && len(args) > 1 {
		operation += " " + args[1]
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("capture nix %s diagnostics: %w", operation, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start nix %s: %w", operation, err)
	}

	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	var writeErr error
	for scanner.Scan() {
		if err := handleLine(scanner.Text()); err != nil && writeErr == nil {
			writeErr = err
		}
	}
	scanErr := scanner.Err()
	runErr := cmd.Wait()
	if scanErr != nil {
		return nil, fmt.Errorf("read nix %s diagnostics: %w", operation, scanErr)
	}
	if writeErr != nil {
		return nil, fmt.Errorf("write nix diagnostics: %w", writeErr)
	}
	if runErr != nil {
		return nil, fmt.Errorf("nix %s failed: %w", operation, runErr)
	}
	return output.Bytes(), nil
}

func printNixLine(line string) error {
	_, err := fmt.Fprintln(os.Stderr, line)
	return err
}

func evaluationID(kind, name string) string {
	return kind + "\x00" + name
}

func nixString(value string) string {
	return `"` + strings.NewReplacer(
		`\`, `\\`, `"`, `\"`, `${`, `\${`,
	).Replace(value) + `"`
}
