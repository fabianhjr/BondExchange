{ pkgs, ... }:

let
  # nixpkgs currently wraps TLA+ with Java 8. Use the same JDK that the
  # development shell exposes so local runs and CI share one Java runtime.
  tlaPlus = pkgs.tlaplus.override { jre8 = pkgs.jdk21_headless; };

  gremlins = pkgs.buildGoModule rec {
    pname = "gremlins";
    version = "0.6.0";

    src = pkgs.fetchFromGitHub {
      owner = "go-gremlins";
      repo = "gremlins";
      rev = "v${version}";
      hash = "sha256-QwMj7aA4eafMT25gBLAomZMliCbueoEsDHD/nxtnmk4=";
    };

    vendorHash = "sha256-TYbbDN2V6GLj+YRNQIKggCnNspk3M96cP1DSe8P9qlY=";
    subPackages = [ "cmd/gremlins" ];
  };

  postgresHarness = pkgs.writeShellApplication {
    name = "bond-exchange-with-postgres";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.dbmate
      pkgs.postgresql_17
      pkgs.util-linux
    ];
    text = builtins.readFile ./nix/with-postgres.sh;
  };

  postgresLifecycleCheck = pkgs.writeShellApplication {
    name = "bond-exchange-postgres-lifecycle-check";
    runtimeInputs = [
      pkgs.bash
      pkgs.coreutils
      pkgs.postgresql_17
      postgresHarness
    ];
    text = builtins.readFile ./nix/postgres-lifecycle-check.sh;
  };

  demoServe = pkgs.writeShellApplication {
    name = "bond-exchange-demo-serve";
    runtimeInputs = [
      pkgs.go
      pkgs.postgresql_17
      pkgs.stdenv.cc
    ];
    text = builtins.readFile ./nix/demo-serve.sh;
  };

  demo = pkgs.writeShellApplication {
    name = "bond-exchange-demo";
    runtimeInputs = [
      demoServe
      postgresHarness
    ];
    text = ''
      export BOND_EXCHANGE_DATABASE_NAME=bond_exchange_demo
      exec bond-exchange-with-postgres bond-exchange-demo-serve
    '';
  };

  demoSmokeCheck = pkgs.writeShellApplication {
    name = "bond-exchange-demo-smoke-check";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.curl
      pkgs.grpcurl
      pkgs.jq
      demo
    ];
    text = builtins.readFile ./nix/demo-smoke-check.sh;
  };

  goTest = pkgs.writeShellApplication {
    name = "bond-exchange-go-test";
    runtimeInputs = [
      pkgs.go
      pkgs.stdenv.cc
    ];
    text = "go test -race ./...";
  };

  goCoverage = pkgs.writeShellApplication {
    name = "bond-exchange-go-coverage";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.gawk
      pkgs.go
      pkgs.stdenv.cc
    ];
    text = ''
      mkdir -p .artifacts
      go test \
        -covermode=atomic \
        -coverpkg=./internal/... \
        -coverprofile=.artifacts/coverage.out \
        ./internal/...
      coverage="$(go tool cover -func=.artifacts/coverage.out | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')"
      echo "Statement coverage: $coverage%"
      awk -v coverage="$coverage" 'BEGIN { if (coverage + 0 < 90) exit 1 }'
    '';
  };

  goMutation = pkgs.writeShellApplication {
    name = "bond-exchange-go-mutation";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.go
      pkgs.stdenv.cc
      gremlins
    ];
    text = ''
      mkdir -p .artifacts
      gremlins unleash
    '';
  };
in
{
  packages = [
    pkgs.buf
    pkgs.dbmate
    pkgs.go
    pkgs.gawk
    pkgs.grpc-gateway
    pkgs.grpcurl
    pkgs.jdk21_headless
    pkgs.nixfmt
    pkgs.postgresql_17
    pkgs.protobuf
    pkgs.protoc-gen-go
    pkgs.protoc-gen-go-grpc
    pkgs.shellcheck
    tlaPlus
    gremlins
    postgresHarness
    demo
  ];

  env = {
    DBMATE_MIGRATIONS_DIR = "./db/migrations";
    DBMATE_NO_DUMP_SCHEMA = "true";
    DBMATE_STRICT = "true";
  };

  processes.demo.exec = "${demo}/bin/bond-exchange-demo";

  tasks."db:migrate" = {
    description = "Validate migrations against a fresh temporary PostgreSQL database";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres true";
  };

  tasks."dev:check" = {
    description = "Check Nix formatting and lifecycle shell scripts";
    exec = ''
      nixfmt --check devenv.nix
      shellcheck nix/*.sh
    '';
    before = [ "devenv:enterTest" ];
  };

  tasks."go:check" = {
    description = "Check Go formatting and run go vet";
    exec = ''
      unformatted="$(gofmt -l cmd gen internal)"
      if [ -n "$unformatted" ]; then
        echo "The following Go files need formatting:"
        echo "$unformatted"
        exit 1
      fi
      go vet ./...
    '';
    after = [
      "api:check"
      "dev:check"
    ];
  };

  tasks."api:generate" = {
    description = "Generate Go, gRPC-Gateway, and Swagger artifacts from Proto3";
    cwd = "./api";
    exec = "buf generate";
  };

  tasks."api:update-deps" = {
    description = "Update the locked remote Proto3 schema dependencies";
    cwd = "./api";
    exec = "buf dep update";
  };

  tasks."api:check" = {
    description = "Lint Proto3 and verify that generated API artifacts are current";
    exec = ''
      snapshot_api() {
        find gen/go api/openapi -type f -print0 2>/dev/null \
          | sort -z \
          | xargs -0 sha256sum
      }
      before="$(snapshot_api)"
      buf lint api
      (cd api && buf generate)
      after="$(snapshot_api)"
      if [ "$before" != "$after" ]; then
        echo "Generated API artifacts were stale; review and commit the regenerated files."
        exit 1
      fi
    '';
    before = [ "devenv:enterTest" ];
  };

  tasks."go:test" = {
    description = "Run Go tests, including PostgreSQL integration tests, with the race detector";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres ${goTest}/bin/bond-exchange-go-test";
    after = [
      "go:check"
    ];
  };

  tasks."go:coverage" = {
    description = "Require at least 90% statement coverage for internal Go packages";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres ${goCoverage}/bin/bond-exchange-go-coverage";
    after = [ "go:test" ];
  };

  tasks."go:mutation" = {
    description = "Require at least 80% mutation-test efficacy";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres ${goMutation}/bin/bond-exchange-go-mutation";
    after = [
      "demo:smoke"
      "go:coverage"
    ];
    before = [ "devenv:enterTest" ];
  };

  tasks."postgres:lifecycle-check" = {
    description = "Verify temporary PostgreSQL isolation and cleanup";
    exec = "${postgresLifecycleCheck}/bin/bond-exchange-postgres-lifecycle-check";
    after = [ "dev:check" ];
    before = [ "devenv:enterTest" ];
  };

  tasks."demo:smoke" = {
    description = "Start and exercise the disposable local demo environment";
    exec = "${demoSmokeCheck}/bin/bond-exchange-demo-smoke-check";
    after = [
      "go:check"
      "postgres:lifecycle-check"
    ];
    before = [ "devenv:enterTest" ];
  };

  tasks."dev:smoke" = {
    description = "Run PostgreSQL lifecycle and local demo smoke checks";
    exec = "true";
    after = [
      "demo:smoke"
      "postgres:lifecycle-check"
    ];
  };

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
