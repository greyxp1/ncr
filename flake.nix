{
  description = "A tool for reporting evaluation time and closure size for Nix configurations";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  outputs = {
    self,
    nixpkgs,
    ...
  }: let
    systems = [
      "x86_64-linux"
      "aarch64-linux"
      "aarch64-darwin"
    ];
  in {
    devShells = nixpkgs.lib.genAttrs systems (system: let
      pkgs = nixpkgs.legacyPackages.${system};
    in {
      default = pkgs.mkShell {
        packages = [pkgs.cargo pkgs.rustc pkgs.rustfmt pkgs.clippy pkgs.unixtools.script];
      };
    });

    packages =
      nixpkgs.lib.genAttrs systems
      (system: let
        pkgs = nixpkgs.legacyPackages.${system};
      in rec {
        nix-closure-report = pkgs.callPackage ./nix/package.nix {};
        default = nix-closure-report;
      });

    nixosModules.default = import ./nix/nixos-module.nix {inherit self;};

    checks.x86_64-linux.module = let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      evaluated = nixpkgs.lib.nixosSystem {
        inherit system;
        modules = [
          self.nixosModules.default
          {
            programs.ncr = {
              enable = true;
              package = pkgs.hello;
              flake = "/home/test/nixconf";
            };
            boot.isContainer = true;
            system.stateVersion = "26.05";
          }
        ];
      };
      disabled = evaluated.extendModules {
        modules = [{programs.ncr.enable = nixpkgs.lib.mkForce false;}];
      };
      missingFlake = evaluated.extendModules {
        modules = [{programs.ncr.flake = nixpkgs.lib.mkForce null;}];
      };
      valid = system: builtins.all (entry: entry.assertion) system.config.assertions;
    in
      assert evaluated.config.environment.variables.NCR_FLAKE == "/home/test/nixconf";
      assert builtins.elem pkgs.hello evaluated.config.environment.systemPackages;
      assert valid evaluated;
      assert !(disabled.config.environment.variables ? NCR_FLAKE);
      assert !(builtins.elem pkgs.hello disabled.config.environment.systemPackages);
      assert valid disabled;
      assert !(valid missingFlake);
        pkgs.runCommand "ncr-module-test" {} "touch $out";
  };
}
