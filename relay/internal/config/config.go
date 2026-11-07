package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr         string
	PublicURL          string
	DatabaseURL        string
	EnrollmentMode     string
	DataKey            []byte
	LogLevel           string
	TrustedProxyCIDRs  []string
	ShutdownTimeout    time.Duration
	ACLCacheTTL        time.Duration
	MaxConnections     int
	MaxDaemons         int
	APNsTeamID         string
	APNsKeyID          string
	APNsKeyFile        string
	FCMCredentialsFile string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:         envDefault("RELAY_LISTEN_ADDR", ":8080"),
		PublicURL:          strings.TrimSpace(os.Getenv("RELAY_PUBLIC_URL")),
		DatabaseURL:        strings.TrimSpace(os.Getenv("DATABASE_URL")),
		EnrollmentMode:     envDefault("ENROLLMENT_MODE", "self_service"),
		LogLevel:           envDefault("LOG_LEVEL", "info"),
		ShutdownTimeout:    20 * time.Second,
		ACLCacheTTL:        30 * time.Second,
		MaxConnections:     10_000,
		MaxDaemons:         10_000,
		APNsTeamID:         strings.TrimSpace(os.Getenv("APNS_TEAM_ID")),
		APNsKeyID:          strings.TrimSpace(os.Getenv("APNS_KEY_ID")),
		APNsKeyFile:        strings.TrimSpace(os.Getenv("APNS_KEY_FILE")),
		FCMCredentialsFile: strings.TrimSpace(os.Getenv("FCM_CREDENTIALS_FILE")),
	}
	if raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXY_CIDRS")); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			if item = strings.TrimSpace(item); item != "" {
				cfg.TrustedProxyCIDRs = append(cfg.TrustedProxyCIDRs, item)
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv("RELAY_SHUTDOWN_TIMEOUT")); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("invalid RELAY_SHUTDOWN_TIMEOUT")
		}
		cfg.ShutdownTimeout = value
	}
	if raw := strings.TrimSpace(os.Getenv("RELAY_MAX_CONNECTIONS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("invalid RELAY_MAX_CONNECTIONS")
		}
		cfg.MaxConnections = value
	}
	if raw := strings.TrimSpace(os.Getenv("RELAY_MAX_DAEMONS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return Config{}, errors.New("invalid RELAY_MAX_DAEMONS")
		}
		cfg.MaxDaemons = value
	}
	if parsed, err := url.Parse(cfg.PublicURL); err == nil && parsed.Host != "" {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		if (parsed.Scheme == "https" && parsed.Port() == "443") || (parsed.Scheme == "http" && parsed.Port() == "80") {
			parsed.Host = parsed.Hostname()
			if strings.Contains(parsed.Host, ":") {
				parsed.Host = "[" + parsed.Host + "]"
			}
		}
		if parsed.Path == "/" {
			parsed.Path = ""
		}
		cfg.PublicURL = parsed.String()
	}
	key, err := loadDataKey()
	if err != nil {
		return Config{}, err
	}
	cfg.DataKey = key
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	if c.PublicURL == "" {
		return errors.New("RELAY_PUBLIC_URL is required")
	}
	parsed, err := url.Parse(c.PublicURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("RELAY_PUBLIC_URL must be an HTTP or HTTPS origin without path, query, or fragment")
	}
	switch c.EnrollmentMode {
	case "invite", "open", "disabled", "self_service":
	default:
		return errors.New("ENROLLMENT_MODE must be invite, open, disabled, or self_service")
	}
	for _, cidr := range c.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return errors.New("invalid TRUSTED_PROXY_CIDRS")
		}
	}
	if len(c.DataKey) != 32 {
		return errors.New("RELAY_DATA_KEY must decode to exactly 32 bytes")
	}
	if (c.APNsTeamID != "" || c.APNsKeyID != "" || c.APNsKeyFile != "") && (c.APNsTeamID == "" || c.APNsKeyID == "" || c.APNsKeyFile == "") {
		return errors.New("APNS_TEAM_ID, APNS_KEY_ID, and APNS_KEY_FILE must be configured together")
	}
	return nil
}

func loadDataKey() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("RELAY_DATA_KEY"))
	if file := strings.TrimSpace(os.Getenv("RELAY_DATA_KEY_FILE")); file != "" {
		if raw != "" {
			return nil, errors.New("set only one of RELAY_DATA_KEY and RELAY_DATA_KEY_FILE")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read RELAY_DATA_KEY_FILE: %w", err)
		}
		raw = strings.TrimSpace(string(data))
	}
	if raw == "" {
		return nil, errors.New("RELAY_DATA_KEY or RELAY_DATA_KEY_FILE is required")
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("RELAY_DATA_KEY must be standard base64")
	}
	return data, nil
}

func envDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
