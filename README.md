# nix-closure-report

`ncr` reports the closure size of each NixOS
configuration and their deduplicated total.

![Example ncr report](https://github.com/user-attachments/assets/63888ea0-9c14-4c71-92a0-a96913c1e99e)

By default, NCR evaluates and builds every host before printing the report.

## Quick start

From the root of a flake containing `nixosConfigurations`:

```console
nix run github:greyxp1/ncr
```

## Installation

Add the flake to your inputs:

```nix
ncr.url = "github:greyxp1/ncr";
```

## Usage

NCR accepts flake references in the same form as Nix and `nh`:

| Command | Report |
| --- | --- |
| `ncr` | Every host in the current flake |
| `ncr .#desktop` | `desktop` from the current flake |
| `ncr /path/to/flake` | Every host in another local flake |
| `ncr /path/to/flake#desktop` | One host from another local flake |
| `ncr github:owner/repo#desktop` | One host from a remote flake |

Multiple hosts from one flake can also be selected:

```console
ncr /path/to/flake desktop vm
```

## Private binary caches

NCR needs no cache-specific configuration. It automatically uses Nix's
configured substituters, signing keys, and credentials.

Nix downloads cache hits and builds cache misses. A cache can shorten the build
phase, but NCR must still evaluate each selected configuration to determine its
store paths.
