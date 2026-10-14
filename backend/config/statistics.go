package config

import "github.com/sirupsen/logrus"

// StatisticsPersistence contains the config for persisting the in-memory
// statistics (blockyx extension). When enabled, closed hourly buckets are
// written to a SQLite database, so the /api/stats data (dashboard cards)
// survives restarts instead of resetting to zero.
type StatisticsPersistence struct {
	// If true, enables statistics persistence.
	Enable bool `default:"false" yaml:"enable"`
	// Filesystem path of the SQLite database file.
	Path string `yaml:"path"`
	// Hourly buckets older than this are deleted from the database.
	RetentionDays int `default:"30" yaml:"retentionDays"`
	// How often the currently open (partial) hour bucket is flushed to the database.
	FlushInterval Duration `default:"5m" yaml:"flushInterval"`
}

// IsEnabled implements `config.Configurable`.
func (c *StatisticsPersistence) IsEnabled() bool {
	return c.Enable
}

// LogConfig implements `config.Configurable`.
func (c *StatisticsPersistence) LogConfig(logger *logrus.Entry) {
	logger.Infof("enable = %t", c.Enable)
	if c.Enable {
		logger.Infof("path = %s", c.Path)
		logger.Infof("retentionDays = %d", c.RetentionDays)
		logger.Infof("flushInterval = %s", c.FlushInterval)
	}
}

// Statistics contains the config for the in-memory statistics subsystem.
type Statistics struct {
	// If true, enables in-memory statistics collection and the /api/stats endpoint.
	Enable bool `default:"false" yaml:"enable"`
	// Persistence config (blockyx extension).
	Persistence StatisticsPersistence `yaml:"persistence"`
}

// IsEnabled implements `config.Configurable`.
func (c *Statistics) IsEnabled() bool {
	return c.Enable
}

// LogConfig implements `config.Configurable`.
func (c *Statistics) LogConfig(logger *logrus.Entry) {
	logger.Infof("enable = %t", c.Enable)
	logger.Infof("persistence.enable = %t", c.Persistence.Enable)
	if c.Persistence.Enable {
		logger.Infof("persistence.path = %s", c.Persistence.Path)
		logger.Infof("persistence.retentionDays = %d", c.Persistence.RetentionDays)
		logger.Infof("persistence.flushInterval = %s", c.Persistence.FlushInterval)
	}
}
