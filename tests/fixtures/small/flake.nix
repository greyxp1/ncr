{
  outputs = _: let
    system = builtins.currentSystem;
    shell = builtins.storePath (builtins.getEnv "NCR_TEST_BASH");
    small = builtins.derivation {
      name = "ncr-small";
      inherit system;
      builder = shell;
      args = ["-c" ''printf small > "$out"''];
    };
    large = builtins.derivation {
      name = "ncr-large";
      inherit system;
      builder = shell;
      dependency = small;
      args = ["-c" ''printf '%02048d%s' 0 "$dependency" > "$out"''];
    };
    # Only the reported platform is foreign; realization uses the local builder.
    # This exercises --all-systems without cross compilation or remote builders.
    foreign = large // {system = "ncr-foreign";};
  in {
    nixosConfigurations.shared.config.system.build.toplevel = small;
    darwinConfigurations.shared.system = small;
    homeConfigurations = {
      shared.activationPackage = small;
      foreign = {
        pkgs.stdenv.buildPlatform.system = "ncr-foreign";
        activationPackage = throw "foreign activation package was evaluated before filtering";
      };
    };
    systemConfigs = {
      alpha = small;
      beta = small;
      inherit large foreign;
    };
  };
}
