# Map just's names to Go's GOOS/GOARCH
go_os := if os() == "macos" { "darwin" } else { os() }
go_arch := if arch() == "aarch64" { "arm64" } else { arch() }

build:
  go build -o nssh ./cmd/nssh

build-linux:
  GOOS=linux GOARCH=amd64 go build -o nssh-linux ./cmd/nssh

install: build
  cp nssh $HOME/.local/bin/nssh
  codesign -fs - $HOME/.local/bin/nssh

run *args: build
  ./nssh {{ args }}

test:
  go test ./...

# Install nssh on a remote host and set up clipboard/xdg-open symlinks.
setup host *args: build-linux
  bash setup.sh {{host}} {{args}}
