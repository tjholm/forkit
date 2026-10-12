{
  description = "Forkit development shell";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { nixpkgs, ... }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [ go wails bun ]
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
