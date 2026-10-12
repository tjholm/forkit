{
  lib,
  stdenvNoCC,
  runCommand,
  buildGoApplication,
  bun2nix,
  git,
  gtk3,
  webkitgtk_4_1,
  pkg-config,
  wrapGAppsHook3,
  makeDesktopItem,
  copyDesktopItems,
  version,
}:
let
  src = lib.cleanSource ../.;

  # bun.lock records each package's integrity hash, so bun.nix is generated from
  # it at evaluation time rather than committed.
  bunNix = runCommand "forkit-bun.nix" { nativeBuildInputs = [ bun2nix ]; } ''
    bun2nix --lock-file ${src}/frontend/bun.lock --output-file $out
  '';

  frontend = stdenvNoCC.mkDerivation {
    pname = "forkit-frontend";
    inherit version;
    src = "${src}/frontend";
    nativeBuildInputs = [ bun2nix.hook ];
    bunDeps = bun2nix.fetchBunDeps { inherit bunNix; };
    buildPhase = ''
      runHook preBuild
      bun run build
      runHook postBuild
    '';
    installPhase = ''
      runHook preInstall
      cp -r dist $out
      runHook postInstall
    '';
  };
in
buildGoApplication {
  pname = "forkit";
  inherit version src;
  pwd = src;
  modules = ../gomod2nix.toml;

  nativeBuildInputs = [ pkg-config wrapGAppsHook3 copyDesktopItems ];
  buildInputs = [ gtk3 webkitgtk_4_1 ];

  preBuild = ''
    cp -r ${frontend} frontend/dist
  '';
  subPackages = [ "." ];
  # The tags `wails build -tags webkit2_41` passes to go build. gomod2nix splits
  # a list of tags on spaces, so they are joined with commas instead.
  tags = [ "desktop,production,webkit2_41" ];
  ldflags = [ "-s" "-w" "-X main.version=${version}" ];
  doCheck = false;

  desktopItems = [
    (makeDesktopItem {
      name = "forkit";
      desktopName = "Forkit";
      genericName = "Git Client";
      exec = "forkit";
      icon = "forkit";
      categories = [ "Development" "RevisionControl" ];
    })
  ];

  postInstall = ''
    install -Dm644 build/appicon.png $out/share/icons/hicolor/512x512/apps/forkit.png
  '';

  preFixup = ''
    gappsWrapperArgs+=(--prefix PATH : ${lib.makeBinPath [ git ]})
  '';

  passthru = { inherit bunNix frontend; };

  meta = {
    description = "Desktop git client that treats worktrees as first-class";
    homepage = "https://github.com/jyecusch/forkit";
    license = lib.licenses.mit;
    platforms = lib.platforms.linux;
    mainProgram = "forkit";
  };
}
