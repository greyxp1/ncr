#[path = "src/cli.rs"]
mod cli;

use clap::{CommandFactory, ValueEnum};
use clap_complete::{Shell, generate_to};
use clap_complete_nushell::Nushell;

fn main() -> std::io::Result<()> {
    println!("cargo:rerun-if-changed=src/cli.rs");
    let out = std::env::var_os("OUT_DIR").expect("Cargo sets OUT_DIR");
    let mut command = cli::Cli::command();
    clap_mangen::generate_to(command.clone(), &out)?;
    for &shell in Shell::value_variants() {
        generate_to(shell, &mut command, "ncr", &out)?;
    }
    generate_to(Nushell, &mut command, "ncr", &out)?;
    Ok(())
}
