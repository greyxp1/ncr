# nix-closure-report

`ncr` reports evaluation time and closure size for NixOS,
nix-darwin, and standalone Home Manager configurations.

![Example ncr report](https://github.com/user-attachments/assets/ccfb6367-80aa-4838-a6fd-77a58fcb125b)

## Quick start

Run without installing NCR:

```console
nix run github:greyxp1/ncr /path/to/flake
```

If `programs.ncr.flake` is already configured, omit the path:

```console
nix run github:greyxp1/ncr
```

## Installation

Add NCR as a flake input:

```nix
ncr.url = "github:greyxp1/ncr";
```

Then import its NixOS module:

```nix
  {
    imports = [inputs.ncr.nixosModules.default];
    programs.ncr = {
      enable = true;
      flake = "/path/to/your/flake";
    };
  }
```

## Usage

NCR automatically discovers NixOS, nix-darwin, and standalone Home Manager
configurations. Each configuration is evaluated in a separate Nix process so
their evaluation times are directly comparable.

| Command | Behavior |
| --- | --- |
| `ncr` | All current-system configurations; requires `programs.ncr.flake` |
| `ncr desktop` | Every configuration named `desktop` from `programs.ncr.flake` |
| `ncr /path/to/flake desktop vm` | Named configurations from another flake |
| `ncr github:owner/repo#desktop` | A named configuration from a remote flake |
| `ncr nixosConfigurations` | Only NixOS configurations |
| `ncr darwinConfigurations` | Only nix-darwin configurations |
| `ncr homeConfigurations` | Only standalone Home Manager configurations |
| `ncr homeConfigurations.user@desktop` | One configuration from a specific type |
| `ncr --home` | Only standalone Home Manager configurations |
| `ncr --show-skipped` | Include other-system configurations |
| `ncr --all-systems` | Attempt configurations for every system |
| `ncr --help` | Print usage and exit |
| `ncr --version` | Print version and exit |

### Garbage collection

Garbage collection removes Nix store data reused during evaluation, so the
first evaluation afterward can take much longer. The NixOS module warms the
evaluation automatically after the scheduled garbage collectors succeed:

- `nix.gc.automatic` — the NixOS garbage collector service
- `programs.nh.clean` — nh's scheduled cleanup service

Each success triggers `ncr --warm-only` against `programs.ncr.flake`, restoring
the evaluation cache before your next report. Manual `nix store gc` or
`nix-collect-garbage` runs are not hooked, so the next report is slower once.

To keep manual garbage collection warm too, define an alias that runs
nh's cleanup and then warms NCR:

```zsh
alias clean='nh clean all --optimise --keep 1 && ncr --warm-only'
```

Without the module, warm the evaluation manually:

```console
nix run github:greyxp1/ncr -- --warm-only /path/to/flake
```

The path can again be omitted when `programs.ncr.flake` is configured.

## Private binary caches

NCR uses Nix's configured substituters, signing keys, and credentials. Caches
can shorten realization, but each selected configuration must still be
evaluated.

## Testing

```console
./tests/integration.sh
```
