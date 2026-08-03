{
  description = "Reproducible Go dev environment with build and test support";

  # ============================================================
  # INPUTS
  # ============================================================

  inputs = rec {
    nixpkgs-stable.url = "github:NixOS/nixpkgs/nixos-25.11";
    nixpkgs-unstable.url = "github:NixOS/nixpkgs/nixos-unstable";
    nixpkgs = nixpkgs-unstable;
    nixpkgs-sqlc.url = "github:NixOS/nixpkgs/0ad6f47ea4fe188f4bc8f0380f93ae8523337c6c";
    flake-utils.url = "github:numtide/flake-utils";
  };

  # ============================================================
  # OUTPUTS
  # ============================================================

  outputs =
    inputs@{ self
    , nixpkgs
    , nixpkgs-stable
    , nixpkgs-unstable
    , nixpkgs-sqlc
    , flake-utils
    , ...
    }:
    let
      # ==========================================================
      # PROJECT CONFIGURATION — edit this section for your project
      # ==========================================================

      # Package metadata
      pname = "myproject";
      version = "0.1.0";

      # Go package attribute (e.g., go, go_1_24)
      # Set to null to use the default Go version from nixpkgs
      goAttr = null;

      # Vendor hash for buildGoModule (run `nix build` once with
      # lib.fakeHash to get the real hash from the error message)
      vendorHash = null; # null = vendored in repo; otherwise sha256 string

      # Extra CLI tools available in the dev shell
      devTools = pkgs: pkgsSqlc: with pkgs; [
        gopls                # LSP
        gotools              # goimports, godoc, etc.
        go-tools             # staticcheck
        delve                # debugger
        ast-grep             # structural code search and lint
        golangci-lint        # linter suite
        sqlite               # CLI for inspecting analytics store
        pkgsSqlc.sqlc        # query codegen, pinned to the committed generator version
        # protobuf           # protoc
        # protoc-gen-go      # protobuf Go codegen
        # temporal-cli       # Temporal dev server
        # tlaplus18          # TLC model checker
      ];

      # Native build dependencies (C libraries, system packages)
      # gcc is required for tree-sitter CGo bindings (github.com/tree-sitter/go-tree-sitter)
      nativeBuildDeps = pkgs: with pkgs; [
        gcc
        # pkg-config
        # openssl
        # sqlite
      ];

      # Extra check commands run during `nix build` after go test
      extraCheckPhase = ''
        # go vet ./...
        # staticcheck ./...
      '';

      # Files to install alongside the binary (relative to src)
      extraInstallPhase = ''
        # mkdir -p $out/share/policies
        # cp authz/policies/*.rego $out/share/policies/
      '';

      # ==========================================================
      # IMPLEMENTATION — you shouldn't need to edit below here
      # ==========================================================

      mkOutputs = nixpkgs-channel:
        flake-utils.lib.eachDefaultSystem (system:
          let
            pkgs = import nixpkgs-channel {
              inherit system;
              config.allowUnfree = true;
            };

            pkgsSqlc = import nixpkgs-sqlc { inherit system; };

            goPackage = if goAttr != null
              then pkgs.${goAttr}
              else pkgs.go;

            # ----------------------------------------------------------
            # Build
            # ----------------------------------------------------------

            package = pkgs.buildGoModule {
              inherit pname version;
              src = ./.;
              inherit vendorHash;

              nativeBuildInputs = nativeBuildDeps pkgs;

              checkPhase = ''
                runHook preCheck
                go test -race ./...
                ${extraCheckPhase}
                runHook postCheck
              '';

              postInstall = extraInstallPhase;
            };

            # ----------------------------------------------------------
            # Development Shell
            # ----------------------------------------------------------

            devShell = pkgs.mkShell {
              name = "${pname}-dev";
              inputsFrom = [ package ];
              packages = (devTools pkgs pkgsSqlc);

              shellHook = ''
                echo "Go $(go version | cut -d' ' -f3) dev shell"
                export CGO_ENABLED=1
              '';
            };

          in {
            packages.default = package;
            packages.${pname} = package;

            devShells.default = devShell;

            # Quick check: nix flake check
            checks.build = package;
          }
        );
    in
    mkOutputs nixpkgs;
}
