{
  description = "Redes MCP Server";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
  };

  outputs = {nixpkgs, ...}: let
    # System types to support.
    supportedSystems = ["x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin"];

    # Helper function to generate an attrset '{ x86_64-linux = f "x86_64-linux"; ... }'.
    forAllSystems = nixpkgs.lib.genAttrs supportedSystems;

    # Nixpkgs instantiated for supported system types.
    nixpkgsFor = forAllSystems (system: import nixpkgs {inherit system;});
  in {
    devShells = forAllSystems (system: let
      pkgs = nixpkgsFor.${system};
      # playwright = pkgs.callPackage ./playwright.nix {};
    in {
      default = pkgs.mkShell {
        packages = [
          pkgs.go
          pkgs.gotools
          pkgs.go-tools

          pkgs.delve
          pkgs.gdlv
          pkgs.golangci-lint

          # Others MCP Servers
          pkgs.mcp-nixos
          pkgs.github-mcp-server
          pkgs.playwright-mcp
          # playwright
        ];

        nativeBuildInputs = [
          pkgs.playwright-driver.browsers
        ];

        shellHook = ''
          export PLAYWRIGHT_BROWSERS_PATH=${pkgs.playwright-driver.browsers}
          export PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS=true
          export PLAYWRIGHT_LAUNCH_OPTIONS_EXECUTABLE_PATH=${pkgs.playwright-driver.browsers}/chromium-1181/chrome-linux/chrome;
        '';
      };
    });
  };
}
