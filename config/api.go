package config

import (
	"github.com/sirupsen/logrus"
)

// API holds the HTTP API authentication settings (blockyx extension).
type API struct {
	// APIKey, when set, requires API key authentication for all /api/* endpoints.
	// Clients must send the key either as X-API-Key header or as
	// Authorization: Bearer <key> header.
	// It can also be provided via the BLOCKYX_API_KEY environment variable,
	// which takes precedence over the config file value.
	// The blockyx binary refuses to start when no key is configured.
	APIKey Secret `yaml:"apiKey"`
}

// IsEnabled implements `config.Configurable`: authentication is enabled when a key is set.
func (c *API) IsEnabled() bool {
	return c.APIKey.Reveal() != ""
}

// LogConfig implements `config.Configurable`.
func (c *API) LogConfig(logger *logrus.Entry) {
	if c.IsEnabled() {
		logger.Info("apiKey: <set> (required for /api/*)")
	} else {
		logger.Info("apiKey: <not set> (API endpoints without authentication)")
	}
}
