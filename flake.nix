{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-24.05";
    nixpkgs-unstable.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    gitignore = {
      url = "github:hercules-ci/gitignore.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    xc = {
      url = "github:joerdav/xc";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, nixpkgs-unstable, gomod2nix, gitignore, xc }:
    let
      allSystems = [
        "x86_64-linux" # 64-bit Intel/AMD Linux
        "aarch64-linux" # 64-bit ARM Linux
        "x86_64-darwin" # 64-bit Intel macOS
        "aarch64-darwin" # 64-bit ARM macOS
      ];

      unstableNixPkgs = system: import nixpkgs-unstable {
        inherit system;
      };

      # Wrap ollama so that we can set environment variables to provide models.
      wrappedOllama = system: pkgs:
        let
          #TODO: When https://github.com/ollama/ollama/pull/7001 is merged and the unstable 
          # nixpkgs uses the version with it, we can remove the src and vendorHash overrides,
          # keeping the acceleration override.
          ollama = (pkgs.ollama.overrideAttrs {
            version = "3.11-patch";
            src = pkgs.fetchFromGitHub {
              owner = "a-h";
              repo = "ollama";
              rev = "42e790d02524f5f461eb241d88de12cf6d9afdb2";
              fetchSubmodules = true;
              hash = "sha256-R7KT1Vg4VRtoI1lXBiIKbQJQfxn6sAYXBwAisl1MN5c=";
            };
            vendorHash = "sha256-hSxcREAujhvzHVNwnRTfhi0MKI3s8HNavER2VLz6SYk=";
          }).override
            (oldAttrs: {
              acceleration =
                if system == "aarch64-darwin" || system == "x86_64-darwin" # If darwin, use metal.
                then null
                else "cuda"; # If linux, use cuda. (change manually to "rocm" for AMD GPUs)
            });
          models = pkgs.runCommand "pull-models" { } ''
            export HOME="$out"
            ${ollama}/bin/ollama pull mistral-nemo --local
            ${ollama}/bin/ollama pull nomic-embed-text --local
          '';
          wrapped = pkgs.writeShellScriptBin "ollama" ''
            export HOME="${models}"
            export OLLAMA_MODELS="${models}/.ollama/models"
            exec ${ollama}/bin/ollama "$@"
          '';
        in
        pkgs.symlinkJoin {
          name = "ollama";
          paths = [
            models
            wrapped
            ollama
          ];
        };

      forAllSystems = f: nixpkgs.lib.genAttrs allSystems (system: f {
        system = system;
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            (final: prev: {
              rqlite = prev.pkgs.callPackage ./rqlite.nix { };
            })
            # Use ollama from unstableNixPkgs, because it's a bit more
            # bleeding edge.
            (final: prev: {
              ollama = (wrappedOllama system (unstableNixPkgs system));
            })
          ];
        };
      });

      # Build app.
      app = { name, pkgs, system }: gomod2nix.legacyPackages.${system}.buildGoApplication {
        name = name;
        src = gitignore.lib.gitignoreSource ./.;
        go = pkgs.go;
        # Must be added due to bug https://github.com/nix-community/gomod2nix/issues/120
        pwd = ./.;
        subPackages = [ "cmd/${name}" ];
        CGO_ENABLED = 0;
        flags = [
          "-trimpath"
        ];
        ldflags = [
          "-s"
          "-w"
          "-extldflags -static"
        ];
      };

      # Build Docker containers.
      dockerUser = pkgs: pkgs.runCommand "user" { } ''
        mkdir -p $out/etc
        echo "user:x:1000:1000:user:/home/user:/bin/false" > $out/etc/passwd
        echo "user:x:1000:" > $out/etc/group
        echo "user:!:1::::::" > $out/etc/shadow
      '';
      dockerImage = { name, pkgs, system }: pkgs.dockerTools.buildImage {
        name = name;
        tag = "latest";

        copyToRoot = [
          # Remove coreutils and bash for a smaller container.
          pkgs.coreutils
          pkgs.bash
          (dockerUser pkgs)
          (app { inherit name pkgs system; })
        ];
        config = {
          Cmd = [ name ];
          User = "user:user";
          Env = [ "ADD_ENV_VARIABLES=1" ];
          ExposedPorts = {
            "8080/tcp" = { };
          };
        };
      };

      # Development tools used.
      devTools = { system, pkgs }: [
        pkgs.sqlite # Full text database.
        pkgs.crane
        pkgs.gh
        pkgs.git
        pkgs.go
        xc.packages.${system}.xc
        gomod2nix.legacyPackages.${system}.gomod2nix
        # Database tools.
        pkgs.rqlite # Distributed sqlite.
        pkgs.go-migrate # Migrate database schema.
        # LLM tools.
        pkgs.ollama
      ];

      name = "app";
    in
    {
      # `nix build` builds the app.
      # `nix build .#docker-image` builds the Docker container.
      packages = forAllSystems ({ system, pkgs }: {
        default = app { name = name; pkgs = pkgs; system = system; };
        docker-image = dockerImage { name = name; pkgs = pkgs; system = system; };
      });
      # `nix develop` provides a shell containing required tools.
      # Run `gomod2nix` to update the `gomod2nix.toml` file if Go dependencies change.
      devShells = forAllSystems ({ system, pkgs }: {
        default = pkgs.mkShell {
          buildInputs = (devTools { system = system; pkgs = pkgs; });
        };
      });
    };
}
