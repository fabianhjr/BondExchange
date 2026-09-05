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
      pkgs.postgresql_18
      pkgs.util-linux
    ];
    text = builtins.readFile ./nix/with-postgres.sh;
  };

  postgresLifecycleCheck = pkgs.writeShellApplication {
    name = "bond-exchange-postgres-lifecycle-check";
    runtimeInputs = [
      pkgs.bash
      pkgs.coreutils
      pkgs.postgresql_18
      postgresHarness
    ];
    text = builtins.readFile ./nix/postgres-lifecycle-check.sh;
  };

  demoServe = pkgs.writeShellApplication {
    name = "bond-exchange-demo-serve";
    runtimeInputs = [
      pkgs.go
      pkgs.postgresql_18
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

  demoServerHarness = pkgs.writeShellApplication {
    name = "bond-exchange-with-demo-server";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.curl
      pkgs.go
      pkgs.jq
      pkgs.stdenv.cc
      pkgs.util-linux
      demo
    ];
    text = builtins.readFile ./nix/with-demo-server.sh;
  };

  demoSmokeScenario = pkgs.writeShellApplication {
    name = "bond-exchange-demo-smoke-scenario";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.curl
      pkgs.grpcurl
      pkgs.jq
    ];
    text = builtins.readFile ./nix/demo-smoke-scenario.sh;
  };

  demoSmokeCheck = pkgs.writeShellApplication {
    name = "bond-exchange-demo-smoke-check";
    runtimeInputs = [
      demoServerHarness
      demoSmokeScenario
    ];
    text = builtins.readFile ./nix/demo-smoke-check.sh;
  };

  integrationScenario = pkgs.writeShellApplication {
    name = "bond-exchange-integration-scenario";
    runtimeInputs = [
      pkgs.curl
      pkgs.hurl
      pkgs.jq
    ];
    text = builtins.readFile ./nix/integration-test.sh;
  };

  integrationCheck = pkgs.writeShellApplication {
    name = "bond-exchange-integration-check";
    runtimeInputs = [
      demoServerHarness
      integrationScenario
    ];
    text = ''
      exec bond-exchange-with-demo-server bond-exchange-integration-scenario
    '';
  };

  integrationLoadScenario = pkgs.writeShellApplication {
    name = "bond-exchange-integration-load-scenario";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.curl
      pkgs.jq
      pkgs.vegeta
    ];
    text = builtins.readFile ./nix/integration-load.sh;
  };

  integrationLoad = pkgs.writeShellApplication {
    name = "bond-exchange-integration-load";
    runtimeInputs = [
      demoServerHarness
      integrationLoadScenario
    ];
    text = ''
      exec bond-exchange-with-demo-server bond-exchange-integration-load-scenario
    '';
  };

  goTest = pkgs.writeShellApplication {
    name = "bond-exchange-go-test";
    runtimeInputs = [
      pkgs.go
      pkgs.stdenv.cc
    ];
    text = ''
      application_root="''${DEVENV_ROOT:-$PWD}/application"
      cd "$application_root"
      go test -race ./...
    '';
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
      project_root="''${DEVENV_ROOT:-$PWD}"
      cd "$project_root/application"
      mkdir -p "$project_root/.artifacts"
      go test \
        -covermode=atomic \
        -coverpkg=./internal/... \
        -coverprofile="$project_root/.artifacts/coverage.out" \
        ./internal/...
      coverage="$(go tool cover -func="$project_root/.artifacts/coverage.out" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')"
      echo "Statement coverage: $coverage%"
      awk -v coverage="$coverage" 'BEGIN { if (coverage + 0 < 95) exit 1 }'
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
      project_root="''${DEVENV_ROOT:-$PWD}"
      cd "$project_root/application"
      mkdir -p "$project_root/.artifacts"
      gremlins unleash
    '';
  };

  asvsProfileCheck = pkgs.writeShellApplication {
    name = "bond-exchange-asvs-profile-check";
    runtimeInputs = [ pkgs.gawk ];
    text = builtins.readFile ./nix/asvs-profile-check.sh;
  };

  asvsSourceCheck = pkgs.writeShellApplication {
    name = "bond-exchange-asvs-source-check";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.diffutils
      pkgs.gawk
      pkgs.git
      pkgs.gnugrep
    ];
    text = builtins.readFile ./nix/asvs-source-check.sh;
  };

  docsCheck = pkgs.writeShellApplication {
    name = "bond-exchange-docs-check";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.findutils
      pkgs.gawk
      pkgs.gnugrep
      pkgs.gnused
    ];
    text = builtins.readFile ./nix/docs-check.sh;
  };

  securityCheck = pkgs.writeShellApplication {
    name = "bond-exchange-security-check";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.go
      pkgs.govulncheck
      pkgs.stdenv.cc
      asvsProfileCheck
      asvsSourceCheck
    ];
    text = builtins.readFile ./nix/security-check.sh;
  };

  taskGraphCheck = pkgs.writeShellApplication {
    name = "bond-exchange-task-graph-check";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.gawk
      pkgs.gnugrep
    ];
    text = builtins.readFile ./nix/task-graph-check.sh;
  };

  uuidContractReadiness = pkgs.writeShellApplication {
    name = "bond-exchange-uuid-contract-readiness";
    runtimeInputs = [ pkgs.postgresql_18 ];
    text = builtins.readFile ./nix/uuid-contract-readiness.sh;
  };

  uuidContractHistoryTest = pkgs.writeShellApplication {
    name = "bond-exchange-uuid-contract-history-test";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.dbmate
      pkgs.postgresql_18
    ];
    text = builtins.readFile ./nix/uuid-contract-history-test.sh;
  };

  canonicalMxnReadiness = pkgs.writeShellApplication {
    name = "bond-exchange-canonical-mxn-readiness";
    runtimeInputs = [ pkgs.postgresql_18 ];
    text = builtins.readFile ./nix/canonical-mxn-readiness.sh;
  };

  observabilityCheck = pkgs.writeShellApplication {
    name = "bond-exchange-observability-check";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.go
      pkgs.opentelemetry-collector-contrib
      pkgs.stdenv.cc
    ];
    text = builtins.readFile ./nix/observability-check.sh;
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
    pkgs.hurl
    pkgs.golangci-lint
    pkgs.jdk21_headless
    pkgs.nixfmt
    pkgs.opentelemetry-collector-contrib
    pkgs.postgresql_18
    pkgs.protobuf
    pkgs.protoc-gen-go
    pkgs.protoc-gen-go-grpc
    pkgs.shellcheck
    pkgs.vegeta
    pkgs.govulncheck
    tlaPlus
    gremlins
    postgresHarness
    demo
    integrationCheck
    integrationLoad
    uuidContractReadiness
    uuidContractHistoryTest
    canonicalMxnReadiness
    observabilityCheck
  ];

  env = {
    DBMATE_MIGRATIONS_DIR = "./db/migrations";
    DBMATE_NO_DUMP_SCHEMA = "true";
    DBMATE_STRICT = "true";
  };

  processes.demo.exec = ''
    OTEL_SERVICE_NAME=bond-exchange \
    OTEL_TRACES_EXPORTER=otlp \
    OTEL_METRICS_EXPORTER=otlp \
    OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 \
    OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
    OTEL_METRIC_EXPORT_INTERVAL=5000 \
      ${demo}/bin/bond-exchange-demo
  '';
  processes.otel-collector.exec = "${pkgs.opentelemetry-collector-contrib}/bin/otelcol-contrib --config nix/otel-collector.yaml";

  tasks."db:migrate" = {
    description = "Validate migrations against a fresh temporary PostgreSQL database";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres true";
  };

  tasks."db:uuid-contract-readiness" = {
    description = "Verify UUID/text graph consistency before the contract migration";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres ${uuidContractReadiness}/bin/bond-exchange-uuid-contract-readiness";
  };

  tasks."db:uuid-contract-history" = {
    description = "Verify lossless archival of representative pre-UUID values";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres ${uuidContractHistoryTest}/bin/bond-exchange-uuid-contract-history-test";
    after = [ "db:migrate" ];
  };

  tasks."db:canonical-mxn-readiness" = {
    description = "Verify every active offer has consistent accepted MXN terms";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres ${canonicalMxnReadiness}/bin/bond-exchange-canonical-mxn-readiness";
    after = [ "db:migrate" ];
  };

  tasks."dev:check" = {
    description = "Check Nix formatting, lifecycle shell scripts, and task-graph parity";
    exec = ''
      nixfmt --check devenv.nix
      shellcheck nix/*.sh
      ${taskGraphCheck}/bin/bond-exchange-task-graph-check
    '';
  };

  tasks."docs:check" = {
    description = "Verify documentation links, anchors, indexes, and register identifiers";
    exec = "${docsCheck}/bin/bond-exchange-docs-check";
  };

  tasks."go:check" = {
    description = "Check Go formatting and run curated static analysis";
    exec = ''
      unformatted="$(gofmt -l application/cmd application/gen application/internal)"
      if [ -n "$unformatted" ]; then
        echo "The following Go files need formatting:"
        echo "$unformatted"
        exit 1
      fi
      mkdir -p .artifacts/golangci-lint-cache
      export GOLANGCI_LINT_CACHE="$PWD/.artifacts/golangci-lint-cache"
      (
        cd application
        golangci-lint config verify
        golangci-lint run ./...
      )
    '';
    after = [
      "api:check"
      "dev:check"
    ];
  };

  tasks."api:generate" = {
    description = "Generate Go, gRPC-Gateway, Swagger, and descriptor artifacts from Proto3";
    cwd = "./api";
    exec = ''
      buf generate
      mkdir -p descriptors
      buf build --as-file-descriptor-set -o descriptors/bondexchange.protoset
    '';
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
        find application/gen/go api/openapi api/descriptors -type f -print0 2>/dev/null \
          | sort -z \
          | xargs -0 sha256sum
      }
      before="$(snapshot_api)"
      buf lint api
      (cd api && buf generate && mkdir -p descriptors && buf build --as-file-descriptor-set -o descriptors/bondexchange.protoset)
      after="$(snapshot_api)"
      if [ "$before" != "$after" ]; then
        echo "Generated API artifacts were stale; review and commit the regenerated files."
        exit 1
      fi
    '';
  };

  tasks."go:test" = {
    description = "Run Go tests, including PostgreSQL integration tests, with the race detector";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres ${goTest}/bin/bond-exchange-go-test";
    after = [
      "go:check"
    ];
  };

  tasks."sie:record" = {
    description = "Refresh sanitized Banxico SIE HTTP recordings (requires BANXICO_SIE_TOKEN)";
    exec = ''
      if [ -z "''${BANXICO_SIE_TOKEN:-}" ]; then
        echo "BANXICO_SIE_TOKEN is required to record live SIE interactions." >&2
        exit 1
      fi
      (cd application && go test ./internal/sie -run '^TestRecordLiveSIE$' -count=1)
    '';
  };

  tasks."go:coverage" = {
    description = "Require at least 95% statement coverage for internal Go packages";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres ${goCoverage}/bin/bond-exchange-go-coverage";
    after = [ "go:test" ];
  };

  tasks."go:mutation" = {
    description = "Require at least 95% mutation-test efficacy";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres ${goMutation}/bin/bond-exchange-go-mutation";
    after = [
      "demo:smoke"
      "go:coverage"
    ];
    before = [ "devenv:enterTest" ];
  };

  tasks."security:check" = {
    description = "Validate ASVS evidence, inventory Go modules, and scan Go vulnerabilities";
    exec = "${postgresHarness}/bin/bond-exchange-with-postgres ${securityCheck}/bin/bond-exchange-security-check";
    after = [
      "api:check"
      "db:migrate"
      "spec:check"
    ];
  };

  tasks."postgres:lifecycle-check" = {
    description = "Verify temporary PostgreSQL isolation and cleanup";
    exec = "${postgresLifecycleCheck}/bin/bond-exchange-postgres-lifecycle-check";
    after = [ "dev:check" ];
  };

  tasks."demo:smoke" = {
    description = "Start and exercise the disposable local demo environment";
    exec = "${demoSmokeCheck}/bin/bond-exchange-demo-smoke-check";
    after = [
      "go:check"
      "postgres:lifecycle-check"
    ];
  };

  tasks."integration:test" = {
    description = "Exercise documented sale-offer, listing, and buy interactions over REST";
    exec = "${integrationCheck}/bin/bond-exchange-integration-check";
    after = [
      "go:check"
      "postgres:lifecycle-check"
    ];
  };

  tasks."integration:load-smoke" = {
    description = "Run a small correctness-gated generated HTTP workload";
    exec = ''
      BOND_EXCHANGE_LOAD_COUNT=120 \
      BOND_EXCHANGE_LOAD_RATE=12 \
      BOND_EXCHANGE_LOAD_WORKERS=12 \
        ${integrationLoad}/bin/bond-exchange-integration-load
    '';
    after = [ "integration:test" ];
  };

  tasks."integration:load" = {
    description = "Run configurable generated HTTP load and write reports under .artifacts";
    exec = "${integrationLoad}/bin/bond-exchange-integration-load";
  };

  tasks."observability:check" = {
    description = "Validate the local collector and OpenTelemetry signal contracts";
    exec = "${observabilityCheck}/bin/bond-exchange-observability-check";
    after = [ "go:test" ];
  };

  tasks."dev:smoke" = {
    description = "Run PostgreSQL lifecycle and local demo smoke checks";
    exec = "true";
    after = [
      "demo:smoke"
      "integration:load-smoke"
      "integration:test"
      "postgres:lifecycle-check"
    ];
  };

  # `dev:ci` and `go:mutation` are the only tasks that attach to
  # `devenv:enterTest`. Continuous integration runs them as separate jobs, so
  # `devenv test` and the Go quality workflow cover the same gates by
  # construction. `dev:check` fails when any other task attaches itself here.
  tasks."dev:ci" = {
    description = "Run every quality gate that devenv test runs except mutation testing";
    exec = "true";
    after = [
      "api:check"
      "db:canonical-mxn-readiness"
      "db:migrate"
      "db:uuid-contract-history"
      "demo:smoke"
      "dev:check"
      "dev:smoke"
      "docs:check"
      "go:check"
      "go:coverage"
      "go:test"
      "integration:load-smoke"
      "integration:test"
      "observability:check"
      "postgres:lifecycle-check"
      "security:check"
      "spec:check"
    ];
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
  };
}
