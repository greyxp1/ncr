# nix-closure-report

`ncr` reports evaluation time and closure size for NixOS, nix-darwin, and standalone Home Manager configurations.

![Example ncr report](https://github.com/user-attachments/assets/7b728637-d650-4bae-aaaa-43b84d83c781)

## Quick start

From a flake:

```console
nix run github:greyxp1/ncr .
```

## Flake input

```nix
ncr.url = "github:greyxp1/ncr";
```

## Usage

NCR automatically discovers NixOS, nix-darwin, and standalone Home Manager
configurations.

| Command | Selection |
| --- | --- |
| `ncr .` | All current-system configurations |
| `ncr .#desktop` | Every configuration named `desktop` |
| `ncr /path/to/flake desktop vm` | Named configurations from another flake |
| `ncr github:owner/repo#desktop` | A named configuration from a remote flake |
| `ncr .#nixosConfigurations` | Only NixOS configurations |
| `ncr .#darwinConfigurations` | Only nix-darwin configurations |
| `ncr .#homeConfigurations` | Only standalone Home Manager configurations |
| `ncr .#homeConfigurations.user@desktop` | One configuration from a specific type |
| `ncr --home .` | Only standalone Home Manager configurations |
| `ncr --show-skipped .` | Include other-system configurations |
| `ncr --all-systems .` | Attempt configurations for every system |

## Private binary caches

NCR uses Nix's configured substituters, signing keys, and credentials. Caches
can shorten realization, but each selected configuration must still be
evaluated.

## Testing

```console
./tests/integration.sh
```
