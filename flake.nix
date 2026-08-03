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
    packages =
      nixpkgs.lib.genAttrs systems
      (system: let
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

    nixosModules.default = import ./nixos-module.nix {inherit self;};

    checks.x86_64-linux.module = let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      fakeNcr = pkgs.writeShellScriptBin "ncr" ''
        echo "ncr:$*" >> "$NCR_TEST_LOG"
        exit "''${NCR_TEST_STATUS:-0}"
      '';
      fakeNh = pkgs.writeShellScriptBin "nh" ''
        echo "nh:$*" >> "$NCR_TEST_LOG"
        exit "''${NH_TEST_STATUS:-0}"
      '';
      evaluated = nixpkgs.lib.nixosSystem {
        inherit system;
        modules = [
          self.nixosModules.default
          {
            programs.ncr = {
              enable = true;
              package = fakeNcr;
              nhPackage = fakeNh;
            };
            programs.nh = {
              flake = "/home/test/nixconf";
              clean.enable = true;
            };
            users.users.test = {
              isNormalUser = true;
              home = "/home/test";
            };
            system.stateVersion = "26.05";
          }
        ];
      };
      nh = evaluated.config.programs.nh.package;
      cleanService = evaluated.config.systemd.services.nh-clean;
      warmService = evaluated.config.systemd.services.ncr-warm;
    in
      assert cleanService.environment.NCR_SKIP_WARM == "1";
      assert cleanService.unitConfig.OnSuccess == ["ncr-warm.service"];
      assert warmService.environment.NH_FLAKE == "/home/test/nixconf";
      assert warmService.environment.HOME == "/home/test";
      assert warmService.serviceConfig.User == "test";
        pkgs.runCommand "ncr-module-test" {} ''
          export NCR_TEST_LOG="$TMPDIR/log"
          expect_warm_count() {
            test "$(grep -c '^ncr:' "$NCR_TEST_LOG" || true)" -eq "$1"
          }

          ${nh}/bin/nh os switch
          expect_warm_count 0

          : > "$NCR_TEST_LOG"
          ${nh}/bin/nh -v -e passwordless clean user
          grep -Fxq 'ncr:--warm-only' "$NCR_TEST_LOG"
          expect_warm_count 1

          : > "$NCR_TEST_LOG"
          ${nh}/bin/nh clean all --dry
          NCR_SKIP_WARM=1 ${nh}/bin/nh clean all
          expect_warm_count 0

          : > "$NCR_TEST_LOG"
          if NH_TEST_STATUS=7 ${nh}/bin/nh clean all; then
            exit 1
          else
            test "$?" -eq 7
          fi
          expect_warm_count 0

          : > "$NCR_TEST_LOG"
          if NCR_TEST_STATUS=8 ${nh}/bin/nh clean all; then
            exit 1
          else
            test "$?" -eq 8
          fi
          expect_warm_count 1

          touch "$out"
        '';
  };
}
