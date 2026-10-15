{
  outputs = _: {
    homeConfigurations = {
      broken.activationPackage = throw "intentional evaluation failure: Home Manager";
      foreign = {
        pkgs.stdenv.buildPlatform.system = "ncr-foreign";
        activationPackage = throw "foreign activation package was evaluated before filtering";
      };
    };
    systemConfigs = {
      broken = throw "intentional evaluation failure: first host";
      broken-too = throw "intentional evaluation failure: second host";
      healthy = builtins.derivation {
        name = "ncr-small";
        system = builtins.currentSystem;
        builder = builtins.storePath (builtins.getEnv "NCR_TEST_BASH");
        args = ["-c" ''printf small > "$out"''];
      };
    };
  };
}
