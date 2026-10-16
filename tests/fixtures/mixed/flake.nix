{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    nix-darwin = {
      url = "github:nix-darwin/nix-darwin/master";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = {
    nixpkgs,
    home-manager,
    nix-darwin,
    ...
  }: let
    testSystem = builtins.getEnv "NCR_TEST_SYSTEM";
    mkNixos = system:
      nixpkgs.lib.nixosSystem {
        modules = [
          ({pkgs, ...}: {
            nixpkgs.hostPlatform = system;
            boot.loader.grub.devices = ["nodev"];
            fileSystems."/" = {
              device = "/dev/null";
              fsType = "ext4";
            };
            environment.systemPackages = [pkgs.hello];
            system.stateVersion = "24.11";
          })
        ];
      };
    mkDarwin = system:
      nix-darwin.lib.darwinSystem {
        modules = [
          ({pkgs, ...}: {
            nixpkgs.hostPlatform = system;
            environment.systemPackages = [pkgs.hello];
            system.stateVersion = 6;
          })
        ];
      };
    mkHome = system: let
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
      config // {
        # Verify that NCR skips foreign configurations before forcing this value.
        activationPackage =
          if testSystem == "" || system == testSystem
          then config.activationPackage
          else throw "foreign activation package was evaluated before filtering";
      };

    nixosX86 = mkNixos "x86_64-linux";
    nixosArm = mkNixos "aarch64-linux";
    darwinArm = mkDarwin "aarch64-darwin";
    homeX86 = mkHome "x86_64-linux";
    homeArm = mkHome "aarch64-linux";
    homeDarwin = mkHome "aarch64-darwin";
  in {
    nixosConfigurations = {
      desktop = nixosX86;
      server = nixosArm;
    };
    darwinConfigurations = {
      macbook = darwinArm;
    };
    homeConfigurations = {
      "grey@desktop" = homeX86;
      "grey@server" = homeArm;
      "grey@macbook" = homeDarwin;
    };
  };
}
