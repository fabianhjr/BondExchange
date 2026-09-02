# Bond Exchange

An executable specification of a distributed bond exchange, developed before
the implementation so safety properties can shape the system design.

## Prerequisites

Install [Nix](https://nixos.org/download/) and devenv:

```console
nix profile add nixpkgs#devenv
```

Project dependencies are pinned by `devenv.lock`; Java and TLA+ do not need to
be installed globally.

## Development

Enter the development environment:

```console
devenv shell
```

Run the TLA+ model checker directly:

```console
devenv tasks run spec:check
```

Run the same complete verification command used by CI:

```console
devenv test
```

The formal model and its current abstractions are documented in
[`spec/tla/`](spec/tla/README.md). Architecture decisions are recorded in
[`docs/adr/`](docs/adr/README.md).
