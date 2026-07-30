# nix-closure-report

`ncr` reports evaluation time and closure size for NixOS and standalone Home
Manager configurations, plus their deduplicated total.

![Example ncr report](https://github.com/user-attachments/assets/27073caa-0162-4a07-959d-66630a0053ed)

## Quick start

From a NixOS flake:

```console
nix run github:greyxp1/ncr
```

From a standalone Home Manager flake:

```console
nix run github:greyxp1/ncr -- --home
```

## Flake input

```nix
ncr.url = "github:greyxp1/ncr";
```

## Usage

NCR accepts local and remote flake references:

| Command | Selection |
| --- | --- |
| `ncr` | All NixOS configurations in the current flake |
| `ncr .#desktop` | One NixOS configuration |
| `ncr /path/to/flake desktop vm` | Multiple NixOS configurations |
| `ncr github:owner/repo#desktop` | One configuration from a remote flake |
| `ncr --home` | All standalone Home Manager configurations |
| `ncr --home .#user@desktop` | One standalone Home Manager configuration |
| `ncr --home /path/to/flake user@desktop user@laptop` | Multiple standalone Home Manager configurations |

Home Manager configurations integrated as NixOS modules are already included
in their NixOS system closures.

## Private binary caches

NCR uses Nix's configured substituters, signing keys, and credentials. Caches
can shorten realization, but each selected configuration must still be
evaluated.
