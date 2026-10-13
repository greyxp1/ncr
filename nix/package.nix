{
  installShellFiles,
  lib,
  rustPlatform,
}:
rustPlatform.buildRustPackage {
  pname = "nix-closure-report";
  version = (builtins.fromTOML (builtins.readFile ../Cargo.toml)).package.version;
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../Cargo.toml
      ../Cargo.lock
      ../build.rs
      ../src
      ../LICENSE
    ];
  };
  cargoLock.lockFile = ../Cargo.lock;
  nativeBuildInputs = [installShellFiles];
  postInstall = ''
    for assets in target/*/release/build/ncr-*/out; do
      installManPage "$assets/ncr.1"
      installShellCompletion "$assets"/ncr.{bash,fish,nu} --zsh "$assets/_ncr"
      install -Dm644 "$assets/ncr.elv" "$out/share/elvish/lib/ncr.elv"
      install -Dm644 "$assets/_ncr.ps1" "$out/share/powershell/ncr.Completion.ps1"
    done
    install -Dm644 LICENSE "$out/share/doc/ncr/LICENSE"
  '';
  meta.mainProgram = "ncr";
}
