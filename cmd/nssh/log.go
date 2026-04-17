package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	logFile  *os.File
	logMu    sync.Mutex
	logTopic string
	logSide  string // "session" (local) or persona name (remote shim)
)

// openLog opens the per-topic JSONL log file for appending. Silently no-ops
// if NSSH_LOG=0 or the file can't be opened — logging is best-effort.
func openLog(topic, side string) {
	if os.Getenv("NSSH_LOG") == "0" {
		return
	}
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, "nssh."+topic+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	logFile = f
	logTopic = topic
	logSide = side
}

// logEvent writes one JSONL line. Safe to call before openLog (no-op).
// Line writes are atomic under POSIX O_APPEND for size < PIPE_BUF (~4KB),
// so concurrent shim invocations on the same log don't interleave.
func logEvent(event string, fields map[string]any) {
	if logFile == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	fields["event"] = event
	fields["side"] = logSide
	fields["pid"] = os.Getpid()
	data, err := json.Marshal(fields)
	if err != nil {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	logFile.Write(append(data, '\n'))
}

// logErr logs an error event and also prints to stderr.
func logErr(where string, err error) {
	fmt.Fprintf(os.Stderr, "nssh: %s: %v\n", where, err)
	logEvent("error", map[string]any{"where": where, "err": err.Error()})
}
