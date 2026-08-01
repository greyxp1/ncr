{
  description = "Report evaluation time and closure size for Nix configurations";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  outputs = {nixpkgs, ...}: {
    packages =
      nixpkgs.lib.genAttrs [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ] (system: let
        pkgs = nixpkgs.legacyPackages.${system};
      in rec {
        nix-closure-report = pkgs.buildGoModule {
          pname = "nix-closure-report";
          version = "0.1.0";
          src = pkgs.lib.fileset.toSource {
            root = ./.;
            fileset = pkgs.lib.fileset.unions [
              ./args.go
              ./go.mod
              ./main.go
              ./nix.go
              ./report.go
            ];
          };
          vendorHash = null;
          env.CGO_ENABLED = 0;
          dontPatchELF = true;
          ldflags = ["-s" "-w"];
          nativeBuildInputs = [pkgs.removeReferencesTo];
          # NCR measures durations but never loads time zones.
          postFixup = "remove-references-to -t ${pkgs.tzdata} $out/bin/ncr";
          meta.mainProgram = "ncr";
        };
        default = nix-closure-report;
      });
  };
}
