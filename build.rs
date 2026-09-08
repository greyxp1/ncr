#[path = "src/cli.rs"]
mod cli;

use clap::CommandFactory;
use clap_complete::{Shell, generate_to};

fn main() -> std::io::Result<()> {
    println!("cargo:rerun-if-changed=src/cli.rs");
    let out = std::env::var_os("OUT_DIR").expect("Cargo sets OUT_DIR");
    let mut command = cli::Cli::command();
    clap_mangen::generate_to(command.clone(), &out)?;
    for shell in [Shell::Bash, Shell::Fish, Shell::Zsh] {
        generate_to(shell, &mut command, "ncr", &out)?;
    }
    Ok(())
}
