{
  description = "Forkit, a desktop git client that treats worktrees as first-class";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    bun2nix = {
      url = "github:Mic92/bun2nix/lockfile-v2-v3";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  nixConfig = {
    extra-substituters = [ "https://nix-community.cachix.org" ];
    extra-trusted-public-keys = [
      "nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs="
    ];
  };

  outputs = { self, nixpkgs, gomod2nix, bun2nix }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
      linuxSystems = [ "x86_64-linux" "aarch64-linux" ];
      forSystems = systems: f: nixpkgs.lib.genAttrs systems (system: f system nixpkgs.legacyPackages.${system});
    in
    {
      # The package builds only on Linux, against GTK and WebKitGTK 4.1.
      packages = forSystems linuxSystems (system: pkgs: rec {
        forkit = pkgs.callPackage ./nix/package.nix {
          inherit (gomod2nix.legacyPackages.${system}) buildGoApplication;
          bun2nix = bun2nix.packages.${system}.default;
          version = "0-unstable-${builtins.substring 0 8 self.lastModifiedDate}";
        };
        default = forkit;
      });

      devShells = forSystems systems (system: pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [ go wails bun gomod2nix.packages.${system}.default ]
            ++ pkgs.lib.optionals pkgs.stdenv.hostPlatform.isLinux [ pkg-config gtk3 webkitgtk_4_1 libsoup_3 ];

          # nixpkgs ships WebKitGTK 4.1, which Wails builds against only with the
          # webkit2_41 tag. The dev build keeps its config and cache apart from an
          # installed Forkit's.
          shellHook = pkgs.lib.optionalString pkgs.stdenv.hostPlatform.isLinux ''
            forkit-dev() {
              XDG_CONFIG_HOME=/tmp/forkit-dev/config XDG_CACHE_HOME=/tmp/forkit-dev/cache \
                wails dev -tags webkit2_41 "$@"
            }
          '';
        };
      });
    };
}
