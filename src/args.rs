use anyhow::{Result, bail};
use std::env;

use crate::{HOME, KINDS, Kind, cli::Cli};

pub struct Options {
    pub flake: String,
    pub names: Vec<String>,
    pub kind: Option<Kind>,
    pub jobs: usize,
    pub all_systems: bool,
    pub show_skipped: bool,
}

fn split_configuration(value: &str) -> Option<(Kind, &str)> {
    KINDS.iter().find_map(|&kind| {
        if value == kind.output {
            Some((kind, ""))
        } else {
            value
                .strip_prefix(kind.output)?
                .strip_prefix('.')
                .map(|name| (kind, name))
        }
    })
}

impl Options {
    pub fn resolve(args: Cli) -> Result<Self> {
        let selected_flag = [
            (args.nixos, KINDS[0], "--nixos"),
            (args.nix_darwin, KINDS[1], "--nix-darwin"),
            (args.home, HOME, "--home"),
            (args.system_manager, KINDS[3], "--system-manager"),
        ]
        .into_iter()
        .find(|(enabled, _, _)| *enabled);
        let mut opts = Self {
            flake: String::new(),
            names: Vec::new(),
            kind: selected_flag.map(|(_, kind, _)| kind),
            jobs: args.jobs.map_or_else(
                || std::thread::available_parallelism().map_or(1, |count| count.get()),
                |count| count.get(),
            ),
            all_systems: args.all_systems,
            show_skipped: args.show_skipped,
        };
        let mut targets = args.targets.iter();
        let mut fragment = None;
        if let Some(first) = targets.next() {
            if split_configuration(first).is_some()
                || (first != "." && first != ".." && !first.contains(['/', ':', '#']))
            {
                fragment = Some(first.as_str());
            } else if let Some((flake, name)) = first.split_once('#') {
                opts.flake = if flake.is_empty() { "." } else { flake }.into();
                fragment = (!name.is_empty()).then_some(name);
            } else {
                opts.flake = first.clone();
            }
        }
        for selector in fragment.into_iter().chain(targets.map(String::as_str)) {
            if let Some((kind, name)) = split_configuration(selector) {
                if let Some(selected) = opts.kind
                    && selected != kind
                {
                    if let Some((_, _, flag)) = selected_flag {
                        bail!("{flag} conflicts with {selector:?}");
                    }
                    bail!(
                        "conflicting configuration namespaces: {} and {}",
                        selected.output,
                        kind.output
                    );
                }
                opts.kind = Some(kind);
                if !name.is_empty() {
                    opts.names.push(name.into());
                }
            } else {
                opts.names.push(selector.into());
            }
        }
        if opts.flake.is_empty() {
            opts.flake = env::var("NCR_FLAKE").unwrap_or_default();
            if opts.flake.is_empty() {
                bail!("missing flake reference and NCR_FLAKE is not set");
            }
        }
        Ok(opts)
    }

    pub fn kinds(&self) -> Vec<Kind> {
        self.kind.map_or_else(|| KINDS.to_vec(), |kind| vec![kind])
    }
}
