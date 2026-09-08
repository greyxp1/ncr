{
  flake,
  enabled,
  currentSystem,
  mode,
  kind ? "",
  name ? "",
}: let
  outputs = builtins.getFlake flake;
  project = kind: name: let
    config = outputs.${kind}.${name};
    build =
      if kind == "systemConfigs" then config
      else if kind == "homeConfigurations" then config.activationPackage
      else if kind == "darwinConfigurations" then config.system
      else config.config.system.build.toplevel;
    filterSystem = config.pkgs.stdenv.buildPlatform.system or build.system;
  in
    if currentSystem != "" && filterSystem != currentSystem
    then { system = filterSystem; drv = ""; skipped = true; }
    else { system = build.system; drv = build.drvPath; skipped = false; };
in
  if mode == "discover" then {
    available = builtins.listToAttrs (map (kind: {
      name = kind;
      value = builtins.attrNames (outputs.${kind} or {});
    }) enabled);
    present = builtins.any (kind: builtins.hasAttr kind outputs) enabled;
  }
  else project kind name
