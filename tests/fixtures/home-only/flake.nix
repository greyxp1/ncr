{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = {
    nixpkgs,
    home-manager,
    ...
  }: let
    testSystem = builtins.getEnv "NCR_TEST_SYSTEM";
    mkHome = system:
      let
        config = home-manager.lib.homeManagerConfiguration {
          pkgs = nixpkgs.legacyPackages.${system};
          modules = [
            ({pkgs, ...}: {
              home = {
                username = "ncr";
                homeDirectory =
                  if nixpkgs.lib.hasSuffix "-darwin" system
                  then "/Users/ncr"
                  else "/home/ncr";
                packages = [pkgs.hello];
                stateVersion = "24.11";
              };
            })
          ];
        };
      in
        config
        // {
          # Verify that NCR skips foreign configurations before forcing this value.
          activationPackage =
            if testSystem == "" || system == testSystem
            then config.activationPackage
            else throw "foreign activation package was evaluated before filtering";
        };

    homeX86 = mkHome "x86_64-linux";
    homeArm = mkHome "aarch64-linux";
    homeDarwin = mkHome "aarch64-darwin";
  in {
    homeConfigurations = {
      "grey@desktop" = homeX86;
      "para@desktop" = homeX86;
      "grey@server" = homeArm;
      "para@server" = homeArm;
      "grey@macbook" = homeDarwin;
      "para@macbook" = homeDarwin;
    };
  };
}
