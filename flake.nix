{
  description = "SSH wrapper with xdg-open URL forwarding and one-shot OAuth port proxying";

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

            postInstall = ''
              mv $out/bin/ssh-reverse-ntfy $out/bin/nssh
            '';

            meta = {
              description = "SSH wrapper with xdg-open URL forwarding and one-shot OAuth port proxying";
              homepage = "https://github.com/abizer/ssh-reverse-ntfy";
              license = pkgs.lib.licenses.mit;
              mainProgram = "nssh";
            };
          };
          default = self.packages.${system}.nssh;
        }
      );
    };
}
