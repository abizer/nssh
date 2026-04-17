{
  description = "SSH wrapper with clipboard bridge, xdg-open forwarding, and OAuth port proxying via ntfy";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      forAllSystems = nixpkgs.lib.genAttrs [ "aarch64-darwin" "x86_64-linux" "aarch64-linux" ];
    in
    {
      packages = forAllSystems (system:
        let pkgs = nixpkgs.legacyPackages.${system}; in
        {
          nssh = pkgs.buildGoModule {
            pname = "nssh";
            version = self.shortRev or self.dirtyShortRev or "dev";
            src = self;
            vendorHash = null;
            subPackages = [ "cmd/nssh" ];

            meta = {
              description = "SSH/mosh wrapper with clipboard bridge and xdg-open forwarding via ntfy";
              homepage = "https://github.com/abizer/ssh-reverse-ntfy";
              license = pkgs.lib.licenses.mit;
              mainProgram = "nssh";
            };
          };

          nssh-shim = pkgs.buildGoModule {
            pname = "nssh-shim";
            version = self.shortRev or self.dirtyShortRev or "dev";
            src = self;
            vendorHash = null;
            subPackages = [ "cmd/nssh-shim" ];

            meta = {
              description = "Remote clipboard/xdg-open shim for nssh (symlinked as xclip, wl-copy, etc.)";
              homepage = "https://github.com/abizer/ssh-reverse-ntfy";
              license = pkgs.lib.licenses.mit;
              mainProgram = "nssh-shim";
            };
          };

          default = self.packages.${system}.nssh;
        }
      );
    };
}
