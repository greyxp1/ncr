# nix-closure-report

`ncr` reports evaluation time and closure size for NixOS, nix-darwin, Home Manager, and system-manager configurations.

![Example ncr report](https://github.com/user-attachments/assets/ccfb6367-80aa-4838-a6fd-77a58fcb125b)

## Quick start

Run without installing NCR:

```console
nix run github:greyxp1/ncr /path/to/flake
```

## Installation

Add NCR as a flake input:

```nix
ncr.url = "github:greyxp1/ncr";
```

Import its NixOS module and set your flake path:

```nix
{inputs, ...}: {
  imports = [inputs.ncr.nixosModules.default];
  programs.ncr = {
    enable = true;
    flake = "/path/to/your/flake";
  };
}
```

## Usage

| Command | Behavior |
| --- | --- |
| `ncr` | All current-system configurations from `NCR_FLAKE`, set by `programs.ncr.flake` |
| `ncr <flake>` | Use a flake path or URL |
| `ncr [<host> ...]` | One or more configuration names. Ex: `ncr desktop laptop` |
| `ncr --<type>` | Filter by `nixos`, `home`, `nix-darwin`, or `system-manager` |
| `ncr --jobs N` | Evaluate up to N configurations at once. Defaults to available CPUs |
| `ncr --show-skipped` | List other-system hosts without measuring them |
| `ncr --all-systems` | Attempt to evaluate, build, and measure other-system hosts too |

## Evaluation time

Evaluation time varies with system load and cache state, so the first run after garbage collection may take longer. Evaluating hosts in parallel can finish the report sooner while increasing each host’s evaluation time. For more consistent measurements, evaluate one host at a time or use `--jobs 1` to run sequentially.

## Private binary caches

NCR uses your Nix cache settings and credentials. Binary caches can avoid builds, but each selected configuration still needs evaluation.

## Testing

Run all checks:

```console
nix develop -c ./tests/integration.sh
```
