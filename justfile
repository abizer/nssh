# Map just's names to Go's GOOS/GOARCH
go_os := if os() == "macos" { "darwin" } else { os() }
go_arch := if arch() == "aarch64" { "arm64" } else { arch() }

build:
  go build -o nssh ./cmd/nssh
  go build -o nssh-shim ./cmd/nssh-shim

build-linux:
  GOOS=linux GOARCH=amd64 go build -o nssh-shim-linux ./cmd/nssh-shim

install: build
  cp nssh $HOME/.local/bin/nssh
  codesign -fs - $HOME/.local/bin/nssh

run *args: build
  ./nssh {{ args }}

test:
  go test ./...

# Install the nssh-shim binary and ntfy config on a remote host.
setup host *args: build-linux
  bash setup.sh {{host}} {{args}}
