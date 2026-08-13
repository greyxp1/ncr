{
  description = "A tool for reporting evaluation time and closure size for Nix configurations";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  outputs = {
    self,
    nixpkgs,
    ...
  }: let
    version =
      if self ? shortRev && self.shortRev != null
      then self.shortRev
      else "dev";
    systems = [
      "x86_64-linux"
      "aarch64-linux"
      "aarch64-darwin"
    ];
  in {
    packages =
      nixpkgs.lib.genAttrs systems
      (system: let
        pkgs = nixpkgs.legacyPackages.${system};
      in rec {
        nix-closure-report = pkgs.buildGoModule {
          pname = "nix-closure-report";
          inherit version;
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
          ldflags = ["-s" "-w" "-X main.version=${version}"];
          nativeBuildInputs = [pkgs.removeReferencesTo];
          # NCR measures durations but never loads time zones.
          postFixup = "remove-references-to -t ${pkgs.tzdata} $out/bin/ncr";
          meta.mainProgram = "ncr";
        };
        default = nix-closure-report;
      });

    nixosModules.default = import ./nixos-module.nix {inherit self;};

    checks.x86_64-linux.module = let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      fakeNcr = pkgs.writeShellScriptBin "ncr" ''
        echo "ncr:$*" >> "$NCR_TEST_LOG"
        exit "''${NCR_TEST_STATUS:-0}"
      '';
      mkNcr = {
        nh,
        gc,
      }: nixpkgs.lib.nixosSystem {
        inherit system;
        modules = [
          self.nixosModules.default
          {
            programs.ncr = {
              enable = true;
              package = fakeNcr;
              flake = "/home/test/nixconf";
            };
            programs.nh = nixpkgs.lib.mkIf nh {
              enable = true;
              clean.enable = true;
            };
            nix.gc.automatic = gc;
            users.users.test = {
              isNormalUser = true;
              home = "/home/test";
            };
            system.stateVersion = "26.05";
          }
        ];
      };
      evaluated = mkNcr {
        nh = true;
        gc = false;
      };
      gcEvaluated = mkNcr {
        nh = false;
        gc = true;
      };
      warmService = evaluated.config.systemd.services.ncr-warm;
      cleanService = evaluated.config.systemd.services.nh-clean;
      gcService = gcEvaluated.config.systemd.services.nix-gc;
    in
      assert evaluated.config.environment.variables.NCR_FLAKE == "/home/test/nixconf";
      assert warmService.environment.NCR_FLAKE == "/home/test/nixconf";
      assert warmService.environment.HOME == "/home/test";
      assert warmService.serviceConfig.User == "test";
      assert cleanService.unitConfig.OnSuccess == ["ncr-warm.service"];
      assert gcService.unitConfig.OnSuccess == ["ncr-warm.service"];
        pkgs.runCommand "ncr-module-test" {} "touch $out";
  };
}
