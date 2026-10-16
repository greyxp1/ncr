{
  outputs = _: {
    homeConfigurations.shared.activationPackage = builtins.derivation {
      name = "ncr-small";
      system = builtins.currentSystem;
      builder = builtins.storePath (builtins.getEnv "NCR_TEST_BASH");
      args = ["-c" ''printf small > "$out"''];
    };
  };
}
