{self}: {
  config,
  lib,
  pkgs,
  ...
}: let
  cfg = config.programs.ncr;
  ncrFlake = toString cfg.flake;
in {
  options.programs.ncr = {
    enable = lib.mkEnableOption "NCR";
    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "inputs.ncr.packages.\${pkgs.stdenv.hostPlatform.system}.default";
      description = "The NCR package to install.";
    };
    flake = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = "Default flake whose NixOS, nix-darwin, Home Manager, and system-manager configurations NCR reports on.";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = ncrFlake != "";
        message = "programs.ncr requires programs.ncr.flake";
      }
    ];

    environment.systemPackages = [cfg.package];
    environment.variables.NCR_FLAKE = ncrFlake;
  };
}
