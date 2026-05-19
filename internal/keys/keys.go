// Package keys loads the tabby-sync master key from a configurable
// provider.
//
// Threat model. The master key is the one long-lived secret a
// tabby-sync deployment has: every per-user subkey is derived from
// it via HKDF-SHA256 (see [github.com/kuddy-ai/tabby-sync/internal/crypto]).
// This package is responsible for loading that key from disk or
// from the environment, NOT for protecting the running process. If
// the binary is compromised the key is reachable; if the underlying
// host is compromised the key is reachable. What the package DOES
// guarantee is that a stolen database file alone is not enough to
// recover the encrypted content: an attacker also needs the master
// key. Backing up the master key separately from the database
// (file-mode permissions, off-host backup, secrets manager) is
// therefore an operator responsibility.
//
// Logging. This package emits no log records. Errors returned by
// [Provider.Load] MUST NOT echo the on-disk path, the env-var value,
// the master-key bytes, or any hex thereof; the cli wiring layer
// (see [github.com/kuddy-ai/tabby-sync/internal/cli]) is the only
// place that mentions the path, and even there only via the
// redactPath/scrubPaths helpers. See AGENTS.md §7 and
// docs/LOGGING_POLICY.md.
package keys

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/kuddy-ai/tabby-sync/internal/config"
)

// MasterKeySize is the master-key length in bytes. It mirrors
// [github.com/kuddy-ai/tabby-sync/internal/crypto].KeySize and is
// re-exported here so callers loading the key do not need to import
// the crypto package.
const MasterKeySize = 32

// EnvMasterKey is the environment variable consulted by
// [EnvProvider]. It is exported so cli wiring and tests share one
// source of truth.
const EnvMasterKey = "TABBY_SYNC_MASTER_KEY"

// MasterKeyFilename is the basename of the on-disk file consulted
// by [FileProvider] when [LoadFromConfig] is asked for the "file"
// provider. Centralising the literal here keeps the cli layer's
// scrubPaths argument list in sync with the actual on-disk name.
const MasterKeyFilename = "master.key"

// ErrInvalidLength is returned by [Provider.Load] when the supplied
// master key does not have exactly [MasterKeySize] bytes after
// decoding (raw bytes for [FileProvider], hex-decoded bytes for
// [EnvProvider]). The error MUST NOT echo the offending value,
// the file path, or the actual length.
var ErrInvalidLength = errors.New("keys: master key has wrong length")

// ErrMissing is returned by [Provider.Load] when no master key is
// configured at all (empty env var for [EnvProvider]; see
// [FileProvider] for the file-based equivalent, which auto-generates
// rather than returning this sentinel).
var ErrMissing = errors.New("keys: master key not configured")

// Provider is the contract every master-key source satisfies.
//
// Load returns a freshly allocated 32-byte slice on every call; the
// caller may mutate or zero the returned slice without affecting
// subsequent calls. Implementations MUST NOT log the returned bytes
// and MUST NOT include them in error messages.
type Provider interface {
	Load() ([]byte, error)
}

// LoadFromConfig picks a [Provider] based on cfg.MasterKeyProvider
// and returns both the provider and the loaded key. Returning the
// bytes alongside the provider lets the cli wiring layer hand the
// key directly to
// [github.com/kuddy-ai/tabby-sync/internal/store/encrypted].New
// without invoking Load() a second time.
//
// The supported provider names match the values the config layer
// already validates: "file" rooted at filepath.Join(cfg.DataDir,
// [MasterKeyFilename]) and "env" reading [EnvMasterKey]. Anything
// else returns a non-nil error; callers should treat that as a
// programmer or operator error, not as a runtime sentinel.
func LoadFromConfig(cfg *config.Config) (Provider, []byte, error) {
	if cfg == nil {
		return nil, nil, errors.New("keys: nil config")
	}
	switch cfg.MasterKeyProvider {
	case "file":
		p := NewFileProvider(filepath.Join(cfg.DataDir, MasterKeyFilename))
		key, err := p.Load()
		if err != nil {
			return nil, nil, err
		}
		return p, key, nil
	case "env":
		p := NewEnvProvider(nil) // nil getenv means use os.Getenv at Load time
		key, err := p.Load()
		if err != nil {
			return nil, nil, err
		}
		return p, key, nil
	default:
		return nil, nil, fmt.Errorf("keys: unsupported master_key_provider")
	}
}
