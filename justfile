build goos="" goarch="":
  GOOS={{goos}} GOARCH={{goarch}} go build -o nssh{{if goarch != "" { "-" + goarch } else { "" } }} ./cmd/nssh
  GOOS={{goos}} GOARCH={{goarch}} go build -o nssh-shim{{if goarch != "" { "-" + goarch } else { "" } }} ./cmd/nssh-shim

install: build
  mv nssh $HOME/.local/bin/nssh

build-linux:
  just build linux amd64

run *args: build
  ./nssh {{ args }}

test:
  go test ./...

# Install the nssh-shim binary and ntfy config on a remote host.
# Usage: just setup <host> [extra ssh args...]
setup host *args: build-linux
  bash setup.sh {{host}} {{args}}
