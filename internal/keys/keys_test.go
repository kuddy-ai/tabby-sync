package keys_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/kuddy-ai/tabby-sync/internal/config"
	"github.com/kuddy-ai/tabby-sync/internal/keys"
)

func TestFileProviderCreatesFreshKeyOnFirstCall(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")

	p := keys.NewFileProvider(path)
	got, err := p.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != keys.MasterKeySize {
		t.Fatalf("key len = %d, want %d", len(got), keys.MasterKeySize)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat master key: %v", err)
	}
	if st.Size() != keys.MasterKeySize {
		t.Fatalf("file size = %d, want %d", st.Size(), keys.MasterKeySize)
	}
	if runtime.GOOS != "windows" {
		mode := st.Mode().Perm()
		if mode != 0o600 {
			t.Fatalf("file mode = %o, want 0o600", mode)
		}
		// Parent directory mode must be at most 0o700.
		dst, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat parent dir: %v", err)
		}
		dmode := dst.Mode().Perm()
		if dmode&^0o700 != 0 {
			t.Fatalf("parent dir mode = %o, want subset of 0o700", dmode)
		}
	}
}

func TestFileProviderSecondCallReturnsSameBytes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "master.key")
	p := keys.NewFileProvider(path)
	first, err := p.Load()
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	second, err := p.Load()
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("second Load did not return the same key")
	}
	// Mutating the returned slice must not affect subsequent loads.
	for i := range first {
		first[i] = 0
	}
	third, err := p.Load()
	if err != nil {
		t.Fatalf("third Load: %v", err)
	}
	if !bytes.Equal(second, third) {
		t.Fatal("Load returned an alias to the previous slice")
	}
}

func TestFileProviderRejectsWrongLengthFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0xAB}, keys.MasterKeySize-1), 0o600); err != nil {
		t.Fatalf("seed short master.key: %v", err)
	}
	_, err := keys.NewFileProvider(path).Load()
	if !errors.Is(err, keys.ErrInvalidLength) {
		t.Fatalf("err = %v, want errors.Is(ErrInvalidLength)", err)
	}
}

func TestFileProviderErrorDoesNotLeakPath(t *testing.T) {
	t.Parallel()

	// Pick a path that is guaranteed to fail (parent does not exist
	// AND the parent's parent is a regular file, so MkdirAll fails).
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	bad := filepath.Join(blocker, "subdir", "master.key")
	_, err := keys.NewFileProvider(bad).Load()
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	msg := err.Error()
	for _, leak := range []string{bad, blocker, filepath.Join(blocker, "subdir"), dir} {
		if strings.Contains(msg, leak) {
			t.Fatalf("error leaks path component %q: %q", leak, msg)
		}
	}
}

// TestWrapPathErrorStripsLinkError verifies that os.Rename's
// [*os.LinkError] (not a [*fs.PathError]) cannot expose either path. An older
// wrapPathError that only stripped [*fs.PathError] leaked both the
// canonical master.key path AND the random temp-file path through
// the wrapped error. The cli's scrubPaths can redact the canonical
// path but cannot anticipate the temp-file suffix produced by
// os.CreateTemp, so the only safe place to strip the LinkError is
// inside the provider.
//
// The first iteration of this test seeded the target as a
// non-empty directory and called Load(), but that route
// short-circuits at os.ReadFile(target), which returns
// [*fs.PathError] (with Err: syscall.EISDIR) and never enters
// generate() or os.Rename, so it does not cover this branch. The current shape
// feeds a synthesized [*os.LinkError] directly to the unexported
// wrapPathError helper (re-exported as WrapPathErrorForTest in
// export_test.go) so the rename branch is pinned even if no live
// rename is invoked.
func TestWrapPathErrorStripsLinkError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	canonical := filepath.Join(dir, keys.MasterKeyFilename)
	tmp := filepath.Join(dir, ".master.key.0123456789")
	link := &os.LinkError{
		Op:  "rename",
		Old: tmp,
		New: canonical,
		Err: syscall.EEXIST,
	}

	wrapped := keys.WrapPathErrorForTest("rename master key into place", link)
	if wrapped == nil {
		t.Fatal("expected non-nil wrapped error")
	}
	msg := wrapped.Error()

	// Neither the temp-file nor the canonical path may appear in the
	// wrapped message; the parent directory and the temp-file
	// basename pattern are also forbidden because either alone would
	// leak the on-disk layout to a log scrubber that can only
	// substring-redact known strings.
	for _, leak := range []string{tmp, canonical, dir, ".master.key."} {
		if strings.Contains(msg, leak) {
			t.Fatalf("error leaks path component %q: %q", leak, msg)
		}
	}

	// The wrapped error MUST still surface the underlying syscall
	// error so callers (and operators reading the scrubbed log
	// line) can tell why the rename failed. errors.Is bridges
	// across the %w wrap and across LinkError.Unwrap.
	if !errors.Is(wrapped, syscall.EEXIST) {
		t.Fatalf("errors.Is(wrapped, syscall.EEXIST) = false; wrapped = %v", wrapped)
	}
}

func TestFileProviderConcurrentFirstCall(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "master.key")
	const N = 8
	var wg sync.WaitGroup
	errs := make([]error, N)
	results := make([][]byte, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, err := keys.NewFileProvider(path).Load()
			errs[i] = err
			results[i] = b
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("Load[%d]: %v", i, e)
		}
		if len(results[i]) != keys.MasterKeySize {
			t.Fatalf("Load[%d] len = %d, want %d", i, len(results[i]), keys.MasterKeySize)
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat master key: %v", err)
	}
	if st.Size() != keys.MasterKeySize {
		t.Fatalf("file size = %d, want %d", st.Size(), keys.MasterKeySize)
	}
}

func TestEnvProviderParsesHex(t *testing.T) {
	t.Parallel()

	want := bytes.Repeat([]byte{0xAB}, keys.MasterKeySize)
	value := hex.EncodeToString(want)
	p := keys.NewEnvProvider(func(k string) string {
		if k != keys.EnvMasterKey {
			return ""
		}
		return value
	})
	got, err := p.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("Load returned bytes that do not match the configured hex")
	}
}

func TestEnvProviderRejectsEmpty(t *testing.T) {
	t.Parallel()

	p := keys.NewEnvProvider(func(string) string { return "" })
	_, err := p.Load()
	if !errors.Is(err, keys.ErrMissing) {
		t.Fatalf("err = %v, want errors.Is(ErrMissing)", err)
	}
}

func TestEnvProviderRejectsWrongLengthHex(t *testing.T) {
	t.Parallel()

	short := strings.Repeat("ab", keys.MasterKeySize-1) // valid hex but only 31 bytes when decoded
	p := keys.NewEnvProvider(func(string) string { return short })
	_, err := p.Load()
	if !errors.Is(err, keys.ErrInvalidLength) {
		t.Fatalf("err = %v, want errors.Is(ErrInvalidLength)", err)
	}
}

func TestEnvProviderRejectsNonHex(t *testing.T) {
	t.Parallel()

	bad := "ZZZZ_NOT_HEX_VALUE_VISIBLE_AS_LEAK_SENTINEL"
	p := keys.NewEnvProvider(func(string) string { return bad })
	_, err := p.Load()
	if err == nil {
		t.Fatal("expected non-nil error for non-hex env value")
	}
	if strings.Contains(err.Error(), bad) {
		t.Fatalf("error leaks env value: %q", err.Error())
	}
}

func TestLoadFromConfigDispatch(t *testing.T) {
	// NOTE: this top-level test does NOT call t.Parallel() because the
	// "env" subtest calls t.Setenv, which is incompatible with a
	// parallel parent. The siblings still run sequentially.

	t.Run("file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfg := &config.Config{
			DataDir:           dir,
			MasterKeyProvider: "file",
		}
		_, key, err := keys.LoadFromConfig(cfg)
		if err != nil {
			t.Fatalf("LoadFromConfig: %v", err)
		}
		if len(key) != keys.MasterKeySize {
			t.Fatalf("key len = %d, want %d", len(key), keys.MasterKeySize)
		}
		// File ended up next to cfg.DataDir under MasterKeyFilename.
		st, err := os.Stat(filepath.Join(dir, keys.MasterKeyFilename))
		if err != nil {
			t.Fatalf("master.key not created: %v", err)
		}
		if st.Size() != keys.MasterKeySize {
			t.Fatalf("file size = %d, want %d", st.Size(), keys.MasterKeySize)
		}
	})

	t.Run("env", func(t *testing.T) {
		// Cannot t.Parallel() inside this subtest because we mutate
		// the process env. The parent test already runs in parallel
		// with peers; this subtest is sequential w.r.t. its sibling.
		want := bytes.Repeat([]byte{0xCD}, keys.MasterKeySize)
		t.Setenv(keys.EnvMasterKey, hex.EncodeToString(want))
		cfg := &config.Config{
			DataDir:           t.TempDir(),
			MasterKeyProvider: "env",
		}
		_, key, err := keys.LoadFromConfig(cfg)
		if err != nil {
			t.Fatalf("LoadFromConfig: %v", err)
		}
		if !bytes.Equal(key, want) {
			t.Fatal("env-loaded key does not match the configured value")
		}
	})

	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		cfg := &config.Config{
			DataDir:           t.TempDir(),
			MasterKeyProvider: "vault",
		}
		_, _, err := keys.LoadFromConfig(cfg)
		if err == nil {
			t.Fatal("expected non-nil error for unsupported provider")
		}
	})

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		_, _, err := keys.LoadFromConfig(nil)
		if err == nil {
			t.Fatal("expected non-nil error for nil config")
		}
	})
}
