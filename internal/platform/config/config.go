// Package config reads the runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	Addr           string   // listen address
	Storage        string   // "postgres" or "memory"
	DatabaseURL    string   // required for postgres
	AllowedOrigins []string // CORS
	Timezone       string   // business day zone
	SessionTTL     time.Duration

	// First owner, created only when the system has no users yet.
	BootstrapName     string
	BootstrapEmail    string
	BootstrapPassword string
}

func Load() (Config, error) {
	c := Config{
		Addr:           get("ADDR", ":8080"),
		Storage:        get("STORAGE", "postgres"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		AllowedOrigins: split(get("CORS_ORIGINS", "http://localhost:5173")),
		Timezone:       get("TIMEZONE", "America/Caracas"),

		BootstrapName:     get("BOOTSTRAP_OWNER_NAME", "Dueño"),
		BootstrapEmail:    os.Getenv("BOOTSTRAP_OWNER_EMAIL"),
		BootstrapPassword: os.Getenv("BOOTSTRAP_OWNER_PASSWORD"),
	}
	ttl, err := time.ParseDuration(get("SESSION_TTL", "24h"))
	if err != nil || ttl <= 0 {
		return c, fmt.Errorf("SESSION_TTL must be a positive duration like 24h")
	}
	c.SessionTTL = ttl
	switch c.Storage {
	case "memory":
	case "postgres":
		if c.DatabaseURL == "" {
			return c, fmt.Errorf("DATABASE_URL is required when STORAGE=postgres")
		}
	default:
		return c, fmt.Errorf("STORAGE must be postgres or memory, got %q", c.Storage)
	}
	return c, nil
}

func get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
