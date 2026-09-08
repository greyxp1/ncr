use clap::Parser;

/// Report evaluation time and closure size for Nix configurations.
#[derive(Parser)]
#[command(
    name = "ncr",
    group(clap::ArgGroup::new("kind").args(["nixos", "nix_darwin", "home", "system_manager"])),
    version,
    disable_version_flag = true,
    after_long_help = "Configurations may be selected by name, FLAKE#NAME, or a qualified selector:\n\
        nixosConfigurations, darwinConfigurations, homeConfigurations, or systemConfigs[.NAME].\n\
        Without a flake reference, NCR uses programs.ncr.flake / NCR_FLAKE.\n\n\
        Examples:\n  ncr desktop vm\n  ncr /path/to/flake desktop\n  ncr systemConfigs.alma"
)]
pub struct Cli {
    /// Print version
    #[arg(short = 'v', long, action = clap::ArgAction::Version)]
    pub version: Option<bool>,

    /// Flake reference, configuration names, or a qualified selector
    #[arg(value_name = "FLAKE_OR_CONFIGURATION")]
    pub targets: Vec<String>,

    /// Only NixOS configurations
    #[arg(long)]
    pub nixos: bool,

    /// Only nix-darwin configurations
    #[arg(long)]
    pub nix_darwin: bool,

    /// Only standalone Home Manager configurations
    #[arg(long)]
    pub home: bool,

    /// Only system-manager configurations
    #[arg(long)]
    pub system_manager: bool,

    /// Maximum concurrent evaluations (default: available CPUs; 1 for sequential timing)
    #[arg(short = 'j', long, value_name = "N")]
    pub jobs: Option<std::num::NonZeroUsize>,

    /// Attempt configurations for every system
    #[arg(long)]
    pub all_systems: bool,

    /// Include configurations for other systems
    #[arg(long)]
    pub show_skipped: bool,
}
