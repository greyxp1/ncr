mod args;
mod cli;
mod nix;
mod report;

use anyhow::{Result, bail};
use args::Options;
use clap::Parser;
use report::{Live, Row};
use std::{
    collections::{BTreeMap, BTreeSet},
    io::{self, Write},
};

#[derive(Clone, Copy, PartialEq, Eq)]
pub struct Kind {
    output: &'static str,
    label: &'static str,
}

pub const HOME: Kind = Kind {
    output: "homeConfigurations",
    label: "Home Manager",
};

pub const KINDS: [Kind; 4] = [
    Kind {
        output: "nixosConfigurations",
        label: "NixOS",
    },
    Kind {
        output: "darwinConfigurations",
        label: "nix-darwin",
    },
    HOME,
    Kind {
        output: "systemConfigs",
        label: "system-manager",
    },
];

fn main() {
    if let Err(error) = entry() {
        let _ = writeln!(io::stderr(), "ncr: {error:#}");
        std::process::exit(1);
    }
}

fn entry() -> Result<()> {
    run(Options::resolve(cli::Cli::parse())?)
}

fn run(opts: Options) -> Result<()> {
    let live = Live::new();
    live.set("Evaluating");
    let system = if opts.names.is_empty() && !opts.all_systems {
        nix::system(&live)?
    } else {
        String::new()
    };
    let kinds = opts.kinds();
    let flake = nix::flake_url(&opts.flake, &live)?;
    let evaluator = nix::Evaluator {
        opts: &opts,
        kinds: &kinds,
        flake: &flake,
        system: &system,
        progress: &live,
    };
    let found: nix::Discovery = evaluator.eval("discover", "", "")?;
    if !found.present {
        if kinds.len() == 1 {
            bail!(
                "flake {:?} does not provide {}",
                opts.flake,
                kinds[0].output
            );
        }
        bail!(
            "flake {:?} provides none of nixosConfigurations, darwinConfigurations, homeConfigurations, or systemConfigs",
            opts.flake
        );
    }
    let missing: BTreeSet<_> = opts
        .names
        .iter()
        .filter(|name| !found.available.values().any(|names| names.contains(name)))
        .collect();
    if !missing.is_empty() {
        let available = kinds
            .iter()
            .filter_map(|kind| {
                let names = &found.available[kind.output];
                (!names.is_empty()).then(|| format!("{}: {}", kind.label, names.join(", ")))
            })
            .collect::<Vec<_>>()
            .join("; ");
        bail!(
            "unknown configuration{} {}; available: {}",
            if missing.len() == 1 { "" } else { "s" },
            missing
                .iter()
                .map(|name| format!("{name:?}"))
                .collect::<Vec<_>>()
                .join(", "),
            report::escape_controls(if available.is_empty() {
                "none"
            } else {
                &available
            })
        );
    }
    let configs = evaluator.evaluate(&found.available)?;
    let skipped = configs.iter().filter(|row| row.config.skipped).count();
    if configs.is_empty() {
        bail!("no supported configurations found in {:?}", opts.flake);
    }
    if skipped == configs.len() && !opts.show_skipped {
        let available = kinds
            .iter()
            .filter_map(|kind| {
                let names = configs
                    .iter()
                    .filter(|row| row.kind == *kind)
                    .map(|row| format!("{} ({})", row.name, row.config.system))
                    .collect::<Vec<_>>();
                (!names.is_empty()).then(|| format!("{}: {}", kind.label, names.join(", ")))
            })
            .collect::<Vec<_>>()
            .join("; ");
        bail!(
            "no supported configurations match system {system:?}; available: {}",
            report::escape_controls(&available)
        );
    }
    let realised = if skipped == configs.len() {
        BTreeMap::new()
    } else {
        live.set("Building");
        nix::realise(&configs, &live)?
    };
    let mut closures = BTreeMap::new();
    let mut rows = Vec::new();
    for row in configs {
        let closure = if row.config.skipped {
            if !opts.show_skipped {
                continue;
            }
            None
        } else {
            let path = &realised[&row.config.drv];
            let stats = if let Some(stats) = closures.get(path) {
                *stats
            } else {
                live.set("Measuring");
                let stats = nix::closure(path, &live)?;
                closures.insert(path.clone(), stats);
                stats
            };
            Some(stats)
        };
        rows.push(Row {
            kind: row.kind,
            name: row.name,
            system: row.config.system,
            duration: row.duration,
            closure,
        });
    }
    rows.sort_by(|a, b| {
        let kind_order = |kind: Kind| {
            KINDS
                .iter()
                .position(|candidate| *candidate == kind)
                .unwrap()
        };
        kind_order(a.kind)
            .cmp(&kind_order(b.kind))
            .then_with(|| b.closure.is_some().cmp(&a.closure.is_some()))
            .then_with(|| {
                b.closure
                    .map_or(0, |c| c.size)
                    .cmp(&a.closure.map_or(0, |c| c.size))
            })
            .then_with(|| a.name.cmp(&b.name))
    });
    live.print(&rows, if opts.show_skipped { 0 } else { skipped })
}
