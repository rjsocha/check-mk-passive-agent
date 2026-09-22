package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Tokens []string `json:"tokens"`
}

type Settings struct {
	Listen     string
	Storage    string
	ConfigFile string
	MaxBody    int64
	MaxPayload int64
}

func env(name, fallback string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	return fallback
}

func envSize(name string, fallback int64) (int64, error) {
	v := env(name, "")
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s: invalid size %q", name, v)
	}
	return n, nil
}

func loadSettings() (Settings, error) {
	s := Settings{
		Listen:     env("CMK_AGENTD_LISTEN", "127.0.0.1:8611"),
		Storage:    env("CMK_AGENTD_STORAGE", "/var/lib/monitoring/passive"),
		ConfigFile: "/etc/site/monitoring/agent/config.json",
	}
	if dir := env("CREDENTIALS_DIRECTORY", ""); dir != "" {
		s.ConfigFile = filepath.Join(dir, "config")
	}
	s.ConfigFile = env("CMK_AGENTD_CONFIG", s.ConfigFile)
	var err error
	if s.MaxBody, err = envSize("CMK_AGENTD_MAX_BODY", 8<<20); err != nil {
		return s, err
	}
	if s.MaxPayload, err = envSize("CMK_AGENTD_MAX_PAYLOAD", 32<<20); err != nil {
		return s, err
	}
	return s, nil
}

func loadConfig(path string) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if len(c.Tokens) == 0 {
		return c, errors.New("config: tokens is empty")
	}
	for _, t := range c.Tokens {
		if t == "" {
			return c, errors.New("config: empty token")
		}
	}
	return c, nil
}
