{self}: {
  config,
  lib,
  pkgs,
  ...
}: let
  cfg = config.programs.ncr;
  ncrFlake = toString cfg.flake;
  inferredUsers = lib.attrNames (lib.filterAttrs (
      _: user:
        user.isNormalUser
        && lib.hasPrefix "${user.home}/" "${ncrFlake}/"
    )
    config.users.users);
  warmUser =
    if cfg.user != null
    then cfg.user
    else if builtins.length inferredUsers == 1
    then builtins.head inferredUsers
    else null;
  warmHome =
    if warmUser != null && builtins.hasAttr warmUser config.users.users
    then config.users.users.${warmUser}.home
    else "/root";
in {
  options.programs.ncr = {
    enable = lib.mkEnableOption "NCR with automatic evaluation warmups";
    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "inputs.ncr.packages.\${pkgs.stdenv.hostPlatform.system}.default";
      description = "The NCR package to install.";
    };
    flake = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = ''
        Flake whose NixOS, nix-darwin, and Home Manager configurations NCR
        reports on. Used to warm evaluation without an explicit flake argument.
      '';
    };
    user = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = ''
        User for evaluation warmups. By default, NCR selects the normal user
        whose home contains programs.ncr.flake, or root when no unique user
        matches.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = ncrFlake != "";
        message = "programs.ncr requires programs.ncr.flake";
      }
      {
        assertion = cfg.user == null || builtins.hasAttr cfg.user config.users.users;
        message = "programs.ncr.user must name a configured user";
      }
    ];

    environment.systemPackages = [cfg.package];
    environment.variables.NCR_FLAKE = ncrFlake;

    systemd.services.ncr-warm = {
      description = "Warm NCR evaluation";
      environment = {
        HOME = warmHome;
        NCR_FLAKE = ncrFlake;
        XDG_CACHE_HOME = "${warmHome}/.cache";
      };
      path = [
        config.nix.package
        pkgs.git
      ];
      serviceConfig =
        {
          ExecStart = "${lib.getExe cfg.package} --warm-only";
          Type = "oneshot";
        }
        // lib.optionalAttrs (warmUser != null) {User = warmUser;};
    };

    systemd.services.nh-clean = lib.mkIf config.programs.nh.clean.enable {
      unitConfig.OnSuccess = ["ncr-warm.service"];
    };

    systemd.services.nix-gc = lib.mkIf config.nix.gc.automatic {
      unitConfig.OnSuccess = ["ncr-warm.service"];
    };
  };
}
