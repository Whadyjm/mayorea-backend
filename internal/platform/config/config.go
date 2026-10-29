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

	// RateFeedURL is the public BCV feed the daily rate is read from; "off" disables the sync.
	RateFeedURL      string
	RateSyncInterval time.Duration

	// AccessAllowlist, when not empty, is the only set of emails that may have
	// an account or sign in. Empty means no restriction.
	AccessAllowlist []string

	// Owners that must exist; created at startup if missing (see ParseOwners).
	BootstrapOwners []OwnerSeed
}

// OwnerSeed is an owner account to create on startup if it does not exist.
type OwnerSeed struct {
	Name, Email, Password string
}

// ParseOwners reads "Name|email|password;Name|email|password". Errors never
// include the entry itself, so a password cannot leak into logs.
func ParseOwners(s string) ([]OwnerSeed, error) {
	var out []OwnerSeed
	for i, entry := range strings.Split(s, ";") {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		parts := strings.Split(entry, "|")
		if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || parts[2] == "" {
			return nil, fmt.Errorf("BOOTSTRAP_OWNERS entry %d is malformed (want name|email|password)", i+1)
		}
		out = append(out, OwnerSeed{Name: strings.TrimSpace(parts[0]), Email: strings.TrimSpace(parts[1]), Password: parts[2]})
	}
	return out, nil
}

func Load() (Config, error) {
	c := Config{
		Addr:           get("ADDR", ":8080"),
		Storage:        get("STORAGE", "postgres"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		AllowedOrigins: split(get("CORS_ORIGINS", "http://localhost:5173")),
		Timezone:       get("TIMEZONE", "America/Caracas"),
		RateFeedURL:    get("RATE_FEED_URL", "https://ve.dolarapi.com/v1/dolares/oficial"),

		AccessAllowlist: split(os.Getenv("ACCESS_ALLOWLIST")),
	}
	owners, err := ParseOwners(os.Getenv("BOOTSTRAP_OWNERS"))
	if err != nil {
		return c, err
	}
	c.BootstrapOwners = owners
	ttl, err := time.ParseDuration(get("SESSION_TTL", "24h"))
	if err != nil || ttl <= 0 {
		return c, fmt.Errorf("SESSION_TTL must be a positive duration like 24h")
	}
	c.SessionTTL = ttl
	syncEvery, err := time.ParseDuration(get("RATE_SYNC_INTERVAL", "30m"))
	if err != nil || syncEvery < time.Minute {
		return c, fmt.Errorf("RATE_SYNC_INTERVAL must be a duration of at least 1m, like 30m")
	}
	c.RateSyncInterval = syncEvery
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
