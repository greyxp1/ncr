{self}: {
  config,
  lib,
  pkgs,
  ...
}: let
  cfg = config.programs.ncr;
  nhFlake = toString config.programs.nh.flake;
  inferredUsers = lib.attrNames (lib.filterAttrs (
      _: user:
        user.isNormalUser
        && lib.hasPrefix "${user.home}/" "${nhFlake}/"
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
  wrappedNh = cfg.nhPackage.overrideAttrs (old: {
    buildCommand =
      old.buildCommand
      + ''
        mv "$out/bin/nh" "$out/bin/.nh-ncr"
        substitute ${./nh-wrapper.sh} "$out/bin/nh" \
          --subst-var-by shell ${pkgs.runtimeShell} \
          --subst-var-by nh "$out/bin/.nh-ncr" \
          --subst-var-by ncr ${lib.getExe cfg.package}
        chmod +x "$out/bin/nh"
      '';
  });
in {
  options.programs.ncr = {
    enable = lib.mkEnableOption "NCR with automatic NH cleanup warmups";
    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "inputs.ncr.packages.\${pkgs.stdenv.hostPlatform.system}.default";
      description = "The NCR package to install.";
    };
    nhPackage = lib.mkPackageOption pkgs "nh" {};
    user = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = ''
        User for scheduled evaluation warmups. By default, NCR selects the
        normal user whose home contains programs.nh.flake, or root when no
        unique user matches.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = nhFlake != "";
        message = "programs.ncr requires programs.nh.flake";
      }
      {
        assertion = cfg.user == null || builtins.hasAttr cfg.user config.users.users;
        message = "programs.ncr.user must name a configured user";
      }
    ];

    environment.systemPackages = [cfg.package];
    programs.nh = {
      enable = true;
      package = wrappedNh;
    };

    systemd.services = lib.mkIf config.programs.nh.clean.enable {
      nh-clean = {
        environment.NCR_SKIP_WARM = "1";
        unitConfig.OnSuccess = ["ncr-warm.service"];
      };
      ncr-warm = {
        description = "Warm NCR evaluation after NH cleanup";
        environment = {
          HOME = warmHome;
          NH_FLAKE = nhFlake;
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
    };
  };
}
