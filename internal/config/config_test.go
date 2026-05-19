package config_test

import (
	"strings"
	"testing"

	"github.com/kuddy-ai/tabby-sync/internal/config"
)

// fakeEnv returns a getenv-compatible function backed by an in-memory map.
// Tests use this instead of t.Setenv so they remain parallel-safe and never
// touch real process environment.
func fakeEnv(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func validEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		config.EnvAddr:              "127.0.0.1:9090",
		config.EnvDataDir:           t.TempDir(),
		config.EnvUsersFile:         t.TempDir() + "/users.json",
		config.EnvMasterKeyProvider: "env",
		config.EnvLogLevel:          "info",
	}
}

func TestLoadAllRequiredPresent(t *testing.T) {
	t.Parallel()

	env := validEnv(t)

	cfg, err := config.Load(fakeEnv(env))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9090" {
		t.Errorf("Addr = %q; want %q", cfg.Addr, "127.0.0.1:9090")
	}
	if cfg.DataDir != env[config.EnvDataDir] {
		t.Errorf("DataDir = %q; want %q", cfg.DataDir, env[config.EnvDataDir])
	}
	if cfg.UsersFile != env[config.EnvUsersFile] {
		t.Errorf("UsersFile = %q; want %q", cfg.UsersFile, env[config.EnvUsersFile])
	}
	if cfg.MasterKeyProvider != "env" {
		t.Errorf("MasterKeyProvider = %q; want %q", cfg.MasterKeyProvider, "env")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q; want %q", cfg.LogLevel, "info")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	env := validEnv(t)
	delete(env, config.EnvAddr)
	delete(env, config.EnvLogLevel)

	cfg, err := config.Load(fakeEnv(env))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("default Addr = %q; want %q", cfg.Addr, ":8080")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("default LogLevel = %q; want %q", cfg.LogLevel, "info")
	}
}

func TestLoadMissingDataDir(t *testing.T) {
	t.Parallel()

	env := validEnv(t)
	delete(env, config.EnvDataDir)

	_, err := config.Load(fakeEnv(env))
	if err == nil {
		t.Fatal("Load() expected error for missing TABBY_SYNC_DATA_DIR, got nil")
	}
	if !strings.Contains(err.Error(), config.EnvDataDir) {
		t.Errorf("error %q does not name the variable %q", err.Error(), config.EnvDataDir)
	}
	// Must not echo the values that were set on other variables.
	for _, sensitive := range []string{env[config.EnvUsersFile], env[config.EnvMasterKeyProvider]} {
		if sensitive != "" && strings.Contains(err.Error(), sensitive) {
			t.Errorf("error %q leaks another env value %q", err.Error(), sensitive)
		}
	}
}

func TestLoadMissingUsersFile(t *testing.T) {
	t.Parallel()

	env := validEnv(t)
	delete(env, config.EnvUsersFile)

	_, err := config.Load(fakeEnv(env))
	if err == nil {
		t.Fatal("Load() expected error for missing TABBY_SYNC_USERS_FILE, got nil")
	}
	if !strings.Contains(err.Error(), config.EnvUsersFile) {
		t.Errorf("error %q does not name the variable %q", err.Error(), config.EnvUsersFile)
	}
	if strings.Contains(err.Error(), env[config.EnvDataDir]) {
		t.Errorf("error %q leaks DataDir value", err.Error())
	}
}

func TestLoadInvalidMasterKeyProvider(t *testing.T) {
	t.Parallel()

	env := validEnv(t)
	env[config.EnvMasterKeyProvider] = "vault"

	_, err := config.Load(fakeEnv(env))
	if err == nil {
		t.Fatal("Load() expected error for invalid TABBY_SYNC_MASTER_KEY_PROVIDER, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, config.EnvMasterKeyProvider) {
		t.Errorf("error %q does not name the variable", msg)
	}
	if !strings.Contains(msg, "env") || !strings.Contains(msg, "file") {
		t.Errorf("error %q does not list allowed values env/file", msg)
	}
	if strings.Contains(msg, "vault") {
		t.Errorf("error %q leaks the rejected value", msg)
	}
}

func TestLoadMissingMasterKeyProvider(t *testing.T) {
	t.Parallel()

	env := validEnv(t)
	delete(env, config.EnvMasterKeyProvider)

	_, err := config.Load(fakeEnv(env))
	if err == nil {
		t.Fatal("Load() expected error for missing TABBY_SYNC_MASTER_KEY_PROVIDER, got nil")
	}
	if !strings.Contains(err.Error(), config.EnvMasterKeyProvider) {
		t.Errorf("error %q does not name the variable", err.Error())
	}
}

func TestLoadInvalidLogLevel(t *testing.T) {
	t.Parallel()

	env := validEnv(t)
	env[config.EnvLogLevel] = "trace"

	_, err := config.Load(fakeEnv(env))
	if err == nil {
		t.Fatal("Load() expected error for invalid APP_LOG_LEVEL, got nil")
	}
	if !strings.Contains(err.Error(), config.EnvLogLevel) {
		t.Errorf("error %q does not name the variable", err.Error())
	}
}

func TestLoadNilGetenv(t *testing.T) {
	t.Parallel()

	if _, err := config.Load(nil); err == nil {
		t.Fatal("Load(nil) expected error, got nil")
	}
}

func TestConfigStringRedactsSensitiveFields(t *testing.T) {
	t.Parallel()

	env := validEnv(t)
	cfg, err := config.Load(fakeEnv(env))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	got := cfg.String()

	// Non-sensitive fields are allowed verbatim.
	if !strings.Contains(got, cfg.Addr) {
		t.Errorf("String() = %q; expected to contain Addr %q", got, cfg.Addr)
	}
	if !strings.Contains(got, cfg.LogLevel) {
		t.Errorf("String() = %q; expected to contain LogLevel %q", got, cfg.LogLevel)
	}

	// Sensitive fields must NOT appear verbatim.
	for label, value := range map[string]string{
		"DataDir":           cfg.DataDir,
		"UsersFile":         cfg.UsersFile,
		"MasterKeyProvider": cfg.MasterKeyProvider,
	} {
		if value == "" {
			t.Fatalf("test setup: %s is empty", label)
		}
		if strings.Contains(got, value) {
			t.Errorf("String() = %q; leaks %s value %q", got, label, value)
		}
	}

	// Redaction marker should be present.
	if !strings.Contains(got, "<set>") {
		t.Errorf("String() = %q; expected redaction marker <set>", got)
	}
}

func TestConfigStringNilReceiver(t *testing.T) {
	t.Parallel()

	var c *config.Config
	if got := c.String(); got == "" {
		t.Errorf("(*Config)(nil).String() returned empty string")
	}
}
