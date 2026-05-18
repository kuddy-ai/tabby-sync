// Package config loads tabby-sync runtime configuration from environment
// variables. The loader takes a getenv callback (typically os.Getenv) so it
// can be exercised by unit tests without mutating process state.
//
// Per docs/LOGGING_POLICY.md and AGENTS.md §7, sensitive values such as
// filesystem paths to credential files and the master-key provider name are
// never echoed back in error messages or in the String() representation.
package config

import (
	"errors"
	"fmt"
)

// Default values applied when the corresponding environment variable is
// unset. Required variables have no default and must be provided explicitly.
const (
	defaultAddr     = ":8080"
	defaultLogLevel = "info"
)

// Environment variable names recognised by Load.
const (
	EnvAddr              = "TABBY_SYNC_ADDR"
	EnvDataDir           = "TABBY_SYNC_DATA_DIR"
	EnvUsersFile         = "TABBY_SYNC_USERS_FILE"
	EnvMasterKeyProvider = "TABBY_SYNC_MASTER_KEY_PROVIDER"
	EnvLogLevel          = "APP_LOG_LEVEL"
)

// Allowed values for the master-key provider. Anything else is rejected.
var allowedMasterKeyProviders = []string{"env", "file"}

// Allowed values for the log level. Anything else is rejected.
var allowedLogLevels = []string{"error", "warn", "info", "debug"}

// Config holds the resolved runtime configuration for tabby-sync.
//
// Addr and LogLevel are non-sensitive and may be logged verbatim. DataDir,
// UsersFile, and MasterKeyProvider are sensitive enough that they MUST NOT
// be written to logs as raw values; the String() method below redacts them.
type Config struct {
	Addr              string
	DataDir           string
	UsersFile         string
	MasterKeyProvider string
	LogLevel          string
}

// Load resolves a Config from environment variables using the supplied
// getenv lookup. Passing nil is a programmer error.
//
// Required variables: TABBY_SYNC_DATA_DIR, TABBY_SYNC_USERS_FILE,
// TABBY_SYNC_MASTER_KEY_PROVIDER. Defaults: TABBY_SYNC_ADDR=":8080",
// APP_LOG_LEVEL="info".
//
// Error messages name the offending variable but never echo any environment
// value, so tests can safely assert that error output does not leak inputs.
func Load(getenv func(string) string) (*Config, error) {
	if getenv == nil {
		return nil, errors.New("config.Load: getenv must not be nil")
	}

	cfg := &Config{
		Addr:              getenv(EnvAddr),
		DataDir:           getenv(EnvDataDir),
		UsersFile:         getenv(EnvUsersFile),
		MasterKeyProvider: getenv(EnvMasterKeyProvider),
		LogLevel:          getenv(EnvLogLevel),
	}

	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultLogLevel
	}

	if cfg.DataDir == "" {
		return nil, fmt.Errorf("%s is required", EnvDataDir)
	}
	if cfg.UsersFile == "" {
		return nil, fmt.Errorf("%s is required", EnvUsersFile)
	}
	if cfg.MasterKeyProvider == "" {
		return nil, fmt.Errorf("%s is required (allowed: %v)", EnvMasterKeyProvider, allowedMasterKeyProviders)
	}
	if !contains(allowedMasterKeyProviders, cfg.MasterKeyProvider) {
		// Note: do NOT include the bad value in the error to avoid leaking
		// what the operator actually configured.
		return nil, fmt.Errorf("%s must be one of %v", EnvMasterKeyProvider, allowedMasterKeyProviders)
	}
	if !contains(allowedLogLevels, cfg.LogLevel) {
		return nil, fmt.Errorf("%s must be one of %v", EnvLogLevel, allowedLogLevels)
	}

	return cfg, nil
}

// String returns a redacted, single-line summary of the configuration that
// is safe to log. Sensitive fields (DataDir, UsersFile, MasterKeyProvider)
// are rendered as "<set>" or "<unset>" rather than their raw values.
func (c *Config) String() string {
	if c == nil {
		return "config<nil>"
	}
	return fmt.Sprintf(
		"config{addr=%s log_level=%s data_dir=%s users_file=%s master_key_provider=%s}",
		c.Addr,
		c.LogLevel,
		redact(c.DataDir),
		redact(c.UsersFile),
		redact(c.MasterKeyProvider),
	)
}

func redact(v string) string {
	if v == "" {
		return "<unset>"
	}
	return "<set>"
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
