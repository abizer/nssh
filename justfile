build:
  go build -o nssh ./cmd/nssh

install: build
  mv nssh $HOME/.local/bin/nssh

build-shim:
  GOOS=linux GOARCH=amd64 go build -o nssh-shim ./cmd/nssh-shim

run *args: build
  ./nssh {{ args }}

test:
  go test ./...

# Install the nssh-shim binary and ntfy config on a remote host.
# Usage: just setup <host> [extra ssh args...]
setup host *args: build-shim
  bash setup.sh {{host}} {{args}}
