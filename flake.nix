{
  description = "Report NixOS configuration closure sizes";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = {
    nixpkgs,
    self,
  }: let
    systems = [
      "x86_64-linux"
      "aarch64-linux"
      "x86_64-darwin"
      "aarch64-darwin"
    ];
    forAllSystems = nixpkgs.lib.genAttrs systems;
  in {
    packages = forAllSystems (system: let
      pkgs = nixpkgs.legacyPackages.${system};
      nix-closure-report = pkgs.buildGoModule {
        pname = "nix-closure-report";
        version = "0.1.0";
        src = ./.;
        vendorHash = null;
        env.CGO_ENABLED = 0;
        dontPatchELF = true;
        ldflags = [
          "-s"
          "-w"
        ];
        meta = {
          description = "Report the closure sizes of NixOS configurations";
          homepage = "https://github.com/greyxp1/ncr";
          license = pkgs.lib.licenses.mit;
          mainProgram = "ncr";
        };
      };
    in {
      inherit nix-closure-report;
      default = nix-closure-report;
    });

    apps = forAllSystems (system: {
      ncr = {
        type = "app";
        program = "${self.packages.${system}.nix-closure-report}/bin/ncr";
      };
      default = self.apps.${system}.ncr;
    });

    checks = forAllSystems (system: {
      inherit (self.packages.${system}) nix-closure-report;
    });

    formatter = forAllSystems (system: nixpkgs.legacyPackages.${system}.nixfmt);
  };
}
