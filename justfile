build:
  go build -o nssh .

install: build
  mv nssh $HOME/.local/bin/nssh

run *args: build
  ./nssh {{ args }}

# Install the xdg-open shim and ntfy config on a remote host.
# Usage: just setup <host> [extra ssh args...]
setup host *args:
  bash setup.sh {{host}} {{args}}
