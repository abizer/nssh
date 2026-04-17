package main

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultServer = "https://ntfy.sh"

type nsshConfig struct {
	Server string
	Topic  string
}

// configDir returns ~/.config/nssh (respecting XDG_CONFIG_HOME).
// Used for persistent config: config.toml.
func configDir() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "nssh")
}

// stateDir returns ~/.local/state/nssh (respecting XDG_STATE_HOME).
// Used for ephemeral state: the session file, logs.
func stateDir() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "nssh")
}

// readTOML extracts key="value" pairs from a minimal TOML file.
func readTOML(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	m := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"`)
		m[key] = val
	}
	return m
}

// loadConfig resolves the ntfy server and topic from (in priority order):
//  1. Environment variables (NSSH_NTFY_BASE)
//  2. ~/.config/nssh/config.toml (server, topic) — persistent user config
//  3. ~/.local/state/nssh/session (server, topic) — written by nssh at connect time
//  4. Defaults: server=https://ntfy.sh, topic=<generated>
func loadConfig() nsshConfig {
	cfg := nsshConfig{Server: defaultServer}

	// Session file (written by nssh session mode at connect time).
	session := readTOML(filepath.Join(stateDir(), "session"))
	if session["server"] != "" {
		cfg.Server = session["server"]
	}
	if session["topic"] != "" {
		cfg.Topic = session["topic"]
	}

	// Permanent config overrides session.
	config := readTOML(filepath.Join(configDir(), "config.toml"))
	if config["server"] != "" {
		cfg.Server = config["server"]
	}
	if config["topic"] != "" {
		cfg.Topic = config["topic"]
	}

	// Env var overrides everything for server.
	if v := os.Getenv("NSSH_NTFY_BASE"); v != "" {
		cfg.Server = v
	}

	return cfg
}

// topicURL returns the full ntfy URL for the configured topic.
func (c nsshConfig) topicURL() string {
	return strings.TrimRight(c.Server, "/") + "/" + c.Topic
}

// generateTopic creates a random topic ID: nssh_<20 chars of base32>.
func generateTopic() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: crypto/rand: %v\n", err)
		os.Exit(1)
	}
	id := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return "nssh_" + strings.ToLower(id)
}
