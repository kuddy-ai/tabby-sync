package keys

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// FileProvider reads (or auto-generates) a raw 32-byte master key
// from a caller-supplied filesystem path. The on-disk format is
// exactly [MasterKeySize] raw bytes; there is no header, no encoding,
// and no length prefix. The file is created with mode 0o600 and its
// parent directory with mode 0o700, both enforced explicitly via
// [os.Chmod] so an unusually permissive umask does not leave the key
// world-readable.
//
// The provider does NOT echo the on-disk path in any error string:
// the cli layer is the only place that mentions the path, and only
// via the redactPath/scrubPaths helpers. See AGENTS.md §7.
type FileProvider struct {
	path string
}

// NewFileProvider returns a [FileProvider] rooted at path. The path
// is captured verbatim; resolution of relative paths is the caller's
// responsibility (the cli wiring layer passes filepath.Join with
// cfg.DataDir).
func NewFileProvider(path string) *FileProvider {
	return &FileProvider{path: path}
}

// Load returns the 32-byte master key stored at the configured
// path, generating a new key on first call if the file does not yet
// exist. The first-call path is:
//
//  1. Ensure the parent directory exists with mode 0o700.
//  2. Generate 32 random bytes from crypto/rand.
//  3. Write the bytes to a temporary file in the same directory
//     with mode 0o600.
//  4. Rename the temp file over the target path so a crash mid-way
//     never leaves a half-written master.key on disk.
//  5. Re-chmod the renamed file to 0o600 so the operator's umask
//     does not silently widen permissions on systems where rename
//     does not preserve the temp-file mode.
//
// On the read path, Load verifies the file size is exactly
// [MasterKeySize] bytes and returns [ErrInvalidLength] otherwise
// without echoing the actual length. The returned slice is freshly
// allocated on every call so the caller can zero or hold it without
// affecting subsequent Load() calls.
//
// Errors wrap the underlying syscall error but strip any
// [*fs.PathError.Path] so the on-disk path does not surface in the
// message; this mirrors the pattern in
// [github.com/kuddy-ai/tabby-sync/internal/auth.LoadUsersFile].
func (p *FileProvider) Load() ([]byte, error) {
	raw, err := os.ReadFile(p.path) //nolint:gosec // path comes from the env-validated config; provider does not echo it.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return p.generate()
		}
		return nil, wrapPathError("read master key", err)
	}
	if len(raw) != MasterKeySize {
		return nil, ErrInvalidLength
	}
	out := make([]byte, MasterKeySize)
	copy(out, raw)
	return out, nil
}

// generate writes a fresh master key to the configured path using
// the temp+rename atomic-write pattern documented on [Load].
func (p *FileProvider) generate() ([]byte, error) {
	dir := filepath.Dir(p.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, wrapPathError("create master key parent dir", err)
	}
	// Defence-in-depth: re-chmod the directory in case it pre-existed
	// with a wider mode. Failures here are logged-as-errors but not
	// fatal because os.MkdirAll succeeded; we still want the key
	// file's 0o600 to land. The cli layer never logs the directory
	// path so this stays consistent with the no-leak contract.
	_ = os.Chmod(dir, 0o700) // #nosec G302 -- directories require execute bits; 0700 is owner-only.

	key := make([]byte, MasterKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".master.key.*")
	if err != nil {
		return nil, wrapPathError("create master key temp file", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if any subsequent step fails before the
	// rename. After the rename the temp path no longer exists.
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, wrapPathError("chmod master key temp file", err)
	}
	if _, err := tmp.Write(key); err != nil {
		_ = tmp.Close()
		return nil, wrapPathError("write master key temp file", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, wrapPathError("fsync master key temp file", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, wrapPathError("close master key temp file", err)
	}

	if err := os.Rename(tmpPath, p.path); err != nil {
		return nil, wrapPathError("rename master key into place", err)
	}
	// Re-chmod after the rename; some filesystems / umasks reset the
	// mode on rename, so this is the canonical place where 0o600 is
	// enforced.
	if err := os.Chmod(p.path, 0o600); err != nil {
		return nil, wrapPathError("chmod master key", err)
	}

	out := make([]byte, MasterKeySize)
	copy(out, key)
	return out, nil
}

// wrapPathError strips any path component out of err so the wrapped
// message does not echo the on-disk master.key path, its parent
// directory, or the temporary file used during the atomic
// temp+rename write. errors.Is against the underlying syscall error
// is preserved because the inner error is wrapped with %w.
//
// Two error shapes are handled explicitly. Filesystem syscalls that
// touch a single path return [*fs.PathError]; the rename step that
// enforces 0o600 returns [*os.LinkError], which carries BOTH the
// temp-file path (Old) and the canonical master.key path (New). The LinkError
// branch strips both paths at the provider seam because the CLI cannot know
// the random temp-file suffix produced by os.CreateTemp.
func wrapPathError(op string, err error) error {
	var perr *fs.PathError
	if errors.As(err, &perr) {
		return fmt.Errorf("keys: %s: %w", op, perr.Err)
	}
	var lerr *os.LinkError
	if errors.As(err, &lerr) {
		return fmt.Errorf("keys: %s: %w", op, lerr.Err)
	}
	return fmt.Errorf("keys: %s: %w", op, err)
}
