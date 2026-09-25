// Package config loads neolib's runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SignupPolicy controls who may create accounts.
type SignupPolicy int

const (
	// SignupAuto allows signup only while no user exists (first-run setup).
	SignupAuto SignupPolicy = iota
	// SignupAlways leaves registration open.
	SignupAlways
	// SignupNever closes registration entirely.
	SignupNever
)

// SecureMode controls when cookies are marked Secure.
type SecureMode int

const (
	// SecureAuto marks cookies Secure when the request is HTTPS.
	SecureAuto SecureMode = iota
	SecureAlways
	SecureNever
)

// Config is the complete runtime configuration.
type Config struct {
	Addr           string
	DataDir        string
	MaxUploadBytes int64
	Signup         SignupPolicy
	CookieSecure   SecureMode
}

// FromEnv reads configuration from NEOLIB_* environment variables.
func FromEnv() (Config, error) {
	cfg := Config{
		Addr:           envOr("NEOLIB_ADDR", ":8080"),
		DataDir:        envOr("NEOLIB_DATA_DIR", "data"),
		MaxUploadBytes: 512 << 20,
	}

	abs, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve data directory: %w", err)
	}
	cfg.DataDir = abs

	switch v := strings.ToLower(envOr("NEOLIB_ALLOW_SIGNUP", "auto")); v {
	case "auto":
		cfg.Signup = SignupAuto
	case "true", "1", "yes":
		cfg.Signup = SignupAlways
	case "false", "0", "no":
		cfg.Signup = SignupNever
	default:
		return Config{}, fmt.Errorf("invalid NEOLIB_ALLOW_SIGNUP %q (want auto, true, or false)", v)
	}

	switch v := strings.ToLower(envOr("NEOLIB_SECURE_COOKIES", "auto")); v {
	case "auto":
		cfg.CookieSecure = SecureAuto
	case "true", "1", "yes":
		cfg.CookieSecure = SecureAlways
	case "false", "0", "no":
		cfg.CookieSecure = SecureNever
	default:
		return Config{}, fmt.Errorf("invalid NEOLIB_SECURE_COOKIES %q (want auto, true, or false)", v)
	}

	if v := os.Getenv("NEOLIB_MAX_UPLOAD_MB"); v != "" {
		mb, err := strconv.ParseInt(v, 10, 64)
		if err != nil || mb <= 0 {
			return Config{}, fmt.Errorf("invalid NEOLIB_MAX_UPLOAD_MB %q", v)
		}
		cfg.MaxUploadBytes = mb << 20
	}

	return cfg, nil
}

// DBPath is the SQLite database location.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "db.sqlite") }

// BooksDir holds immutable, content-addressed EPUB files.
func (c Config) BooksDir() string { return filepath.Join(c.DataDir, "books") }

// TempDir holds in-progress uploads. It must share a filesystem with BooksDir.
func (c Config) TempDir() string { return filepath.Join(c.DataDir, "tmp") }

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
