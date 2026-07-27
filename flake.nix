{
  description = "A tool for reporting closure sizes of NixOS configurations";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  outputs = {nixpkgs, ...}: {
    packages =
      nixpkgs.lib.genAttrs [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ] (system: rec {
        nix-closure-report = nixpkgs.legacyPackages.${system}.buildGoModule {
          pname = "nix-closure-report";
          version = "0.1.0";
          src = ./.;
          vendorHash = null;
          env.CGO_ENABLED = 0;
          dontPatchELF = true;
          ldflags = ["-s" "-w"];
          meta.mainProgram = "ncr";
        };
        default = nix-closure-report;
      });
  };
}
