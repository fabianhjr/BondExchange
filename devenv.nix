{ config, pkgs, ... }:

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
    pkgs.postgresql_17
    pkgs.protobuf
    pkgs.protoc-gen-go
    pkgs.protoc-gen-go-grpc
    tlaPlus
    gremlins
  ];

  env = {
    PGDATABASE = "bond_exchange_test";
    DATABASE_URL = "postgresql:///bond_exchange_test?host=${config.env.PGHOST}";
    BOND_EXCHANGE_TEST_DATABASE_URL = "postgresql:///bond_exchange_test?host=${config.env.PGHOST}";
    DBMATE_MIGRATIONS_DIR = "./db/migrations";
    DBMATE_NO_DUMP_SCHEMA = "true";
    DBMATE_STRICT = "true";
  };

  services.postgres = {
    enable = true;
    package = pkgs.postgresql_17;
    initialDatabases = [
      {
        name = "bond_exchange_test";
      }
    ];
  };

  tasks."db:migrate" = {
    description = "Apply pending dbmate migrations in strict version order";
    exec = "dbmate --wait up";
    after = [ "devenv:processes:postgres" ];
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
    after = [ "api:check" ];
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
    exec = "go test -race ./...";
    after = [
      "db:migrate"
      "go:check"
    ];
  };

  tasks."go:coverage" = {
    description = "Require at least 90% statement coverage for internal Go packages";
    exec = ''
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
    after = [ "go:test" ];
  };

  tasks."go:mutation" = {
    description = "Require at least 80% mutation-test efficacy";
    exec = ''
      mkdir -p .artifacts
      gremlins unleash
    '';
    after = [ "go:coverage" ];
    before = [ "devenv:enterTest" ];
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
