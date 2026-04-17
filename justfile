build:
  go build -o nssh ./cmd/nssh

install: build
  cp nssh $HOME/.local/bin/nssh
  codesign -fs - $HOME/.local/bin/nssh

run *args: build
  ./nssh {{ args }}

test:
  go test ./...
