# nix-closure-report

`ncr` reports evaluation time and closure size for NixOS, nix-darwin, and standalone Home Manager configurations.

![Example ncr report](https://github.com/user-attachments/assets/27073caa-0162-4a07-959d-66630a0053ed)

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

NCR accepts local and remote flake references:

| Command | Selection |
| --- | --- |
| `ncr .` | All supported current-system configurations in the current flake |
| `ncr .#desktop` | Every configuration named `desktop` |
| `ncr /path/to/flake desktop vm` | Named configurations from another flake |
| `ncr github:owner/repo#desktop` | Named configurations from a remote flake |
| `ncr --home .` | Only standalone Home Manager configurations |
| `ncr --show-skipped .` | Also show configurations for other systems |
| `ncr --all-systems .` | Configurations for every system |

NCR discovers `nixosConfigurations`, `darwinConfigurations`, and
`homeConfigurations`. Mixed reports identify each configuration in a `type`
column; single-type reports omit the redundant column.
Use `.#nixosConfigurations`, `.#darwinConfigurations`, or
`.#homeConfigurations` to select one type; append a name to select one
configuration, such as `.#homeConfigurations.user@desktop`.

## Private binary caches

NCR uses Nix's configured substituters, signing keys, and credentials. Caches
can shorten realization, but each selected configuration must still be
evaluated.

## Testing

```console
./tests/integration.sh
```
