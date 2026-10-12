// Package config reads the few settings that cannot live in the database.
package config

import (
	"flag"
	"fmt"
	"io"
)

type Config struct {
	Listen  string
	DataDir string
}

// Parse reads `serve` arguments. Flags win over environment variables, which
// win over defaults.
func Parse(args []string, getenv func(string) string) (Config, error) {
	cfg := Config{Listen: "127.0.0.1:8080", DataDir: "data"}
	if v := getenv("ARC5_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := getenv("ARC5_DATA"); v != "" {
		cfg.DataDir = v
	}

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "address to listen on (ARC5_LISTEN)")
	fs.StringVar(&cfg.DataDir, "data", cfg.DataDir, "data folder (ARC5_DATA)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return cfg, nil
}
