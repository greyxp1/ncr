# nix-closure-report

`ncr` reports evaluation time and closure size for NixOS, nix-darwin, and
standalone Home Manager configurations. Its NixOS module installs and enables
[nh](https://github.com/nix-community/nh), allowing NCR to find your
flake from any directory and [warm evaluations after garbage collection](#garbage-collection).

![Example ncr report](https://github.com/user-attachments/assets/ccfb6367-80aa-4838-a6fd-77a58fcb125b)

## Quick start

Run without installing NCR:

```console
nix run github:greyxp1/ncr /path/to/flake
```

If `programs.nh.flake` is already configured, omit the path:

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
modules = [
  ncr.nixosModules.default
  {
    programs = {
      ncr.enable = true;
      nh.flake = "/path/to/your/flake";
    };
  };
];
```

## Usage

NCR automatically discovers NixOS, nix-darwin, and standalone Home Manager
configurations. Each configuration is evaluated in a separate Nix process so
their evaluation times are directly comparable.

| Command | Behavior |
| --- | --- |
| `ncr` | All current-system configurations; requires `programs.nh.flake` |
| `ncr desktop` | Every configuration named `desktop` from `programs.nh.flake` |
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
first evaluation afterward can take much longer. The NixOS module wraps NH so
manual and scheduled `nh clean` commands run `ncr --warm-only` afterward.

Without the module, warm the evaluation manually:

```console
nix run github:greyxp1/ncr -- --warm-only /path/to/flake
```

The path can again be omitted when `programs.nh.flake` is configured.

## Private binary caches

NCR uses Nix's configured substituters, signing keys, and credentials. Caches
can shorten realization, but each selected configuration must still be
evaluated.

## Testing

```console
./tests/integration.sh
```
