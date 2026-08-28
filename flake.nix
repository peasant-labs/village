{
  description = "Reproducible Go dev environment with build and test support";

  # ============================================================
  # INPUTS
  # ============================================================

  inputs = rec {
    nixpkgs-stable.url = "github:NixOS/nixpkgs/nixos-26.05";
    nixpkgs-unstable.url = "github:NixOS/nixpkgs/nixos-unstable";
    nixpkgs = nixpkgs-unstable;
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
    , flake-utils
    , ...
    }:
    let
      # ==========================================================
      # PROJECT CONFIGURATION — edit this section for your project
      # ==========================================================

      # Package metadata
      pname = "village";
      version = "0.1.0";

      # Go package attribute (e.g., go, go_1_24)
      # Set to null to use the default Go version from nixpkgs
      goAttr = null;

      # Vendor hash for buildGoModule (run `nix build` once with
      # lib.fakeHash to get the real hash from the error message)
      vendorHash = null; # null = vendored in repo; otherwise sha256 string

      # Extra CLI tools available in the dev shell
      devTools = pkgs: with pkgs; [
        gopls # LSP
        gotools # goimports, godoc, etc.
        go-tools # staticcheck
        delve # debugger
        ast-grep # structural code search and lint
        golangci-lint # linter suite
        sqlite # CLI for inspecting analytics store
        nodejs_26 # Node.js runtime for frontend tooling
        pnpm # frontend package manager and build tooling
        typescript
        typescript-language-server
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

            goPackage =
              if goAttr != null
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

              # Apache-2.0, matching the committed LICENSE. This is nixpkgs' own
              # `licenses.asl20` attrset (free = true), so the package needs no
              # allowUnfree to build or install. Keep in sync with LICENSE.
              meta = with pkgs.lib; {
                homepage = "https://github.com/peasant-labs/village";
                license = licenses.asl20;
              };
            };

            # ----------------------------------------------------------
            # Development Shell
            # ----------------------------------------------------------

            devShell = pkgs.mkShell {
              name = "${pname}-dev";
              inputsFrom = [ package ];
              packages = (devTools pkgs);

              shellHook = ''
                echo "Go $(go version | cut -d' ' -f3) dev shell"
                export CGO_ENABLED=1
                if [[ -f .envrc.local ]]; then
                  source .envrc.local
                fi
              '';
            };

          in
          {
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
