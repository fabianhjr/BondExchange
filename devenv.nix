{ pkgs, ... }:

let
  # nixpkgs currently wraps TLA+ with Java 8. Use the same JDK that the
  # development shell exposes so local runs and CI share one Java runtime.
  tlaPlus = pkgs.tlaplus.override { jre8 = pkgs.jdk21_headless; };
in
{
  packages = [
    pkgs.jdk21_headless
    tlaPlus
  ];

  tasks."spec:check" = {
    description = "Model-check the Bond Exchange TLA+ specification with TLC";
    cwd = "./spec/tla";
    exec = ''
      tlc \
        -workers 1 \
        -cleanup \
        -metadir "$DEVENV_ROOT/.devenv/tlc" \
        -config BondExchange.cfg \
        BondExchange.tla
    '';
    before = [ "devenv:enterTest" ];
  };
}
