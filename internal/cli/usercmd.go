// Package cli — bootstrap, initialization, user-management, and doctor commands.
package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kuddy-ai/tabby-sync/internal/auth"
	"github.com/kuddy-ai/tabby-sync/internal/keys"
	"github.com/kuddy-ai/tabby-sync/internal/store/sqlite"
)

// tokenBytes is the number of random bytes in a generated token (32 bytes = 256 bits).
const tokenBytes = 32

// tokenPrefix is prepended to every generated token for easy identification.
const tokenPrefixStr = "tbs_"

// usersFileEntry mirrors the on-disk YAML schema for a single user.
type usersFileEntry struct {
	ID          int64  `yaml:"id"`
	Name        string `yaml:"name"`
	TokenPrefix string `yaml:"token_prefix"`
	TokenHash   string `yaml:"token_hash"`
	Disabled    bool   `yaml:"disabled"`
}

// usersFileSchema is the top-level YAML structure.
type usersFileSchema struct {
	Users []usersFileEntry `yaml:"users"`
}

// runInit implements `tabby-sync init`. It creates the data directory,
// generates a master key (if using file provider), and creates a skeleton
// users.yml.
func runInit(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	dataDir := getenv("TABBY_SYNC_DATA_DIR")
	if dataDir == "" {
		fmt.Fprintln(stderr, "error: TABBY_SYNC_DATA_DIR is required")
		return 2
	}
	usersFile := getenv("TABBY_SYNC_USERS_FILE")
	if usersFile == "" {
		usersFile = filepath.Join(dataDir, "users.yml")
	}

	// Create data directory.
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		fmt.Fprintf(stderr, "error: failed to create data directory: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "data directory: %s\n", dataDir)

	// Generate master key if file provider and key doesn't exist.
	provider := getenv("TABBY_SYNC_MASTER_KEY_PROVIDER")
	if provider == "" {
		provider = "file"
	}
	keyPath := filepath.Join(dataDir, keys.MasterKeyFilename)
	if provider == "file" {
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			key := make([]byte, keys.MasterKeySize)
			if _, err := rand.Read(key); err != nil {
				fmt.Fprintf(stderr, "error: failed to generate master key: %v\n", err)
				return 1
			}
			if err := os.WriteFile(keyPath, key, 0o600); err != nil {
				fmt.Fprintf(stderr, "error: failed to write master key: %v\n", err)
				return 1
			}
			fmt.Fprintln(stdout, "master key: generated (BACK THIS UP IMMEDIATELY)")
		} else {
			fmt.Fprintln(stdout, "master key: already exists")
		}
	} else {
		fmt.Fprintf(stdout, "master key: using %s provider\n", provider)
	}

	// Create skeleton users.yml if it doesn't exist.
	if _, err := os.Stat(usersFile); os.IsNotExist(err) {
		skeleton := usersFileSchema{Users: []usersFileEntry{}}
		data, _ := yaml.Marshal(&skeleton)
		if err := os.WriteFile(usersFile, data, 0o600); err != nil {
			fmt.Fprintf(stderr, "error: failed to write users file: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "users file: created at %s\n", usersFile)
	} else {
		fmt.Fprintf(stdout, "users file: already exists at %s\n", usersFile)
	}

	// Create SQLite database.
	dbPath := filepath.Join(dataDir, "tabby-sync.db")
	st, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: failed to initialize database: %v\n", err)
		return 1
	}
	_ = st.Close()
	fmt.Fprintln(stdout, "database: initialized")
	fmt.Fprintln(stdout, "\ninit complete. You can now add users with: tabby-sync user add <name>")
	return 0
}

// runBootstrap implements the non-interactive first-user bootstrap used by
// docker-entrypoint.sh. Unlike `user add`, it never prints the plaintext token:
// the token is written atomically to token.txt with mode 0600 so container logs
// cannot become a credential store.
//
// Name normalisation deliberately uses strings.TrimSpace, the same Unicode
// whitespace semantics used by auth.LoadUsersFile. Keeping this operation in
// Go avoids a shell locale/character-class mismatch that could otherwise write
// a users.yml which the server immediately rejects.
func runBootstrap(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) != 3 {
		fmt.Fprintln(stderr, "Usage: tabby-sync bootstrap <name>")
		return 2
	}

	name := strings.TrimSpace(args[2])
	if name == "" {
		fmt.Fprintln(stderr, "error: name must not be empty")
		return 2
	}

	dataDir := getenv("TABBY_SYNC_DATA_DIR")
	if dataDir == "" {
		fmt.Fprintln(stderr, "error: TABBY_SYNC_DATA_DIR is required")
		return 2
	}
	usersFile := resolveUsersFile(getenv)
	if usersFile == "" {
		fmt.Fprintln(stderr, "error: cannot determine users file path")
		return 2
	}

	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		fmt.Fprintln(stderr, "error: cannot create data directory")
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(usersFile), 0o750); err != nil {
		fmt.Fprintln(stderr, "error: cannot create users-file directory")
		return 1
	}

	if _, err := os.Stat(usersFile); err == nil {
		fmt.Fprintln(stdout, "bootstrap skipped: users file already exists")
		return 0
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(stderr, "error: cannot inspect users file")
		return 1
	}

	token, hash, prefix := generateToken()
	tokenPath := filepath.Join(dataDir, "token.txt")
	if err := writeFileAtomic(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		fmt.Fprintln(stderr, "error: cannot write bootstrap token")
		return 1
	}

	schema := &usersFileSchema{Users: []usersFileEntry{{
		ID:          1,
		Name:        name,
		TokenPrefix: prefix,
		TokenHash:   hash,
		Disabled:    false,
	}}}
	if err := saveUsersYAML(usersFile, schema); err != nil {
		_ = os.Remove(tokenPath)
		fmt.Fprintln(stderr, "error: cannot write bootstrap users file")
		return 1
	}

	fmt.Fprintln(stdout, "bootstrap credentials created")
	return 0
}

// runUser dispatches user subcommands: add, rm, rotate.
func runUser(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) < 3 {
		fmt.Fprintln(stderr, "Usage: tabby-sync user <add|rm|rotate> <name|id>")
		return 2
	}
	subcmd := args[2]
	switch subcmd {
	case "add":
		return runUserAdd(args, getenv, stdout, stderr)
	case "rm", "remove":
		return runUserRm(args, getenv, stdout, stderr)
	case "rotate":
		return runUserRotate(args, getenv, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown user subcommand: %s\n", subcmd)
		fmt.Fprintln(stderr, "Usage: tabby-sync user <add|rm|rotate> <name|id>")
		return 2
	}
}

// runUserAdd implements `tabby-sync user add <name>`.
func runUserAdd(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) < 4 {
		fmt.Fprintln(stderr, "Usage: tabby-sync user add <name>")
		return 2
	}
	name := strings.TrimSpace(args[3])
	if name == "" {
		fmt.Fprintln(stderr, "error: name must not be empty")
		return 2
	}

	usersFile := resolveUsersFile(getenv)
	if usersFile == "" {
		fmt.Fprintln(stderr, "error: cannot determine users file path (set TABBY_SYNC_USERS_FILE or TABBY_SYNC_DATA_DIR)")
		return 2
	}

	schema, err := loadUsersYAML(usersFile)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	// Check for duplicate name.
	for _, u := range schema.Users {
		if strings.EqualFold(u.Name, name) {
			fmt.Fprintf(stderr, "error: user %q already exists (id=%d)\n", name, u.ID)
			return 1
		}
	}

	// Assign next ID.
	var maxID int64
	for _, u := range schema.Users {
		if u.ID > maxID {
			maxID = u.ID
		}
	}
	newID := maxID + 1

	// Generate token.
	token, hash, prefix := generateToken()

	schema.Users = append(schema.Users, usersFileEntry{
		ID:          newID,
		Name:        name,
		TokenPrefix: prefix,
		TokenHash:   hash,
		Disabled:    false,
	})

	if err := saveUsersYAML(usersFile, schema); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "user added:\n")
	fmt.Fprintf(stdout, "  id:    %d\n", newID)
	fmt.Fprintf(stdout, "  name:  %s\n", name)
	fmt.Fprintf(stdout, "  token: %s\n", token)
	fmt.Fprintln(stdout, "\nSave this token now — it will NOT be shown again.")
	fmt.Fprintln(stdout, "Restart the server to pick up the new user.")
	return 0
}

// runUserRm implements `tabby-sync user rm <name|id>`.
// Pass --force to skip the confirmation prompt.
func runUserRm(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) < 4 {
		fmt.Fprintln(stderr, "Usage: tabby-sync user rm [--force] <name|id>")
		return 2
	}

	// Parse --force flag.
	force := false
	target := ""
	for _, arg := range args[3:] {
		if arg == "--force" || arg == "-f" {
			force = true
		} else if target == "" {
			target = strings.TrimSpace(arg)
		}
	}
	if target == "" {
		fmt.Fprintln(stderr, "Usage: tabby-sync user rm [--force] <name|id>")
		return 2
	}

	usersFile := resolveUsersFile(getenv)
	if usersFile == "" {
		fmt.Fprintln(stderr, "error: cannot determine users file path")
		return 2
	}

	schema, err := loadUsersYAML(usersFile)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	idx := findUser(schema, target)
	if idx < 0 {
		fmt.Fprintf(stderr, "error: user %q not found\n", target)
		return 1
	}

	removed := schema.Users[idx]

	if !force {
		fmt.Fprintf(stdout, "about to remove user %s (id=%d). This cannot be undone.\n", removed.Name, removed.ID)
		fmt.Fprint(stdout, "Continue? [y/N] ")
		var answer string
		if _, err := fmt.Fscanln(os.Stdin, &answer); err != nil {
			fmt.Fprintln(stdout, "aborted.")
			return 0
		}
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			fmt.Fprintln(stdout, "aborted.")
			return 0
		}
	}

	schema.Users = append(schema.Users[:idx], schema.Users[idx+1:]...)

	if err := saveUsersYAML(usersFile, schema); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "user removed: %s (id=%d)\n", removed.Name, removed.ID)
	fmt.Fprintln(stdout, "Restart the server to apply changes.")
	return 0
}

// runUserRotate implements `tabby-sync user rotate <name|id>`.
func runUserRotate(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) < 4 {
		fmt.Fprintln(stderr, "Usage: tabby-sync user rotate <name|id>")
		return 2
	}
	target := strings.TrimSpace(args[3])

	usersFile := resolveUsersFile(getenv)
	if usersFile == "" {
		fmt.Fprintln(stderr, "error: cannot determine users file path")
		return 2
	}

	schema, err := loadUsersYAML(usersFile)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	idx := findUser(schema, target)
	if idx < 0 {
		fmt.Fprintf(stderr, "error: user %q not found\n", target)
		return 1
	}

	token, hash, prefix := generateToken()
	schema.Users[idx].TokenHash = hash
	schema.Users[idx].TokenPrefix = prefix

	if err := saveUsersYAML(usersFile, schema); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "token rotated for user %s (id=%d):\n", schema.Users[idx].Name, schema.Users[idx].ID)
	fmt.Fprintf(stdout, "  token: %s\n", token)
	fmt.Fprintln(stdout, "\nSave this token now — it will NOT be shown again.")
	fmt.Fprintln(stdout, "The old token is immediately invalid after server restart.")
	return 0
}

// runDoctor implements `tabby-sync doctor`. It checks the environment for
// common configuration problems.
func runDoctor(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	dataDir := getenv("TABBY_SYNC_DATA_DIR")
	usersFile := getenv("TABBY_SYNC_USERS_FILE")
	provider := getenv("TABBY_SYNC_MASTER_KEY_PROVIDER")

	issues := 0

	// Check TABBY_SYNC_DATA_DIR.
	fmt.Fprintln(stdout, "Checking environment...")
	if dataDir == "" {
		fmt.Fprintln(stdout, "  [FAIL] TABBY_SYNC_DATA_DIR is not set")
		issues++
	} else {
		info, err := os.Stat(dataDir)
		if err != nil {
			fmt.Fprintf(stdout, "  [FAIL] data directory does not exist: %s\n", dataDir)
			issues++
		} else if !info.IsDir() {
			fmt.Fprintf(stdout, "  [FAIL] data directory is not a directory: %s\n", dataDir)
			issues++
		} else {
			// Check writable.
			testFile := filepath.Join(dataDir, ".doctor-probe")
			if err := os.WriteFile(testFile, []byte("ok"), 0o600); err != nil {
				fmt.Fprintf(stdout, "  [FAIL] data directory is not writable: %s\n", dataDir)
				issues++
			} else {
				_ = os.Remove(testFile)
				fmt.Fprintf(stdout, "  [OK]   data directory exists and is writable\n")
			}
		}
	}

	// Check users file.
	if usersFile == "" && dataDir != "" {
		usersFile = filepath.Join(dataDir, "users.yml")
	}
	if usersFile == "" {
		fmt.Fprintln(stdout, "  [FAIL] TABBY_SYNC_USERS_FILE is not set and TABBY_SYNC_DATA_DIR is empty")
		issues++
	} else {
		users, err := auth.LoadUsersFile(usersFile)
		if err != nil {
			fmt.Fprintf(stdout, "  [FAIL] users file: %v\n", err)
			issues++
		} else {
			fmt.Fprintf(stdout, "  [OK]   users file loaded (%d users)\n", users.UserCount())
		}
	}

	// Check master key.
	if provider == "" {
		fmt.Fprintln(stdout, "  [FAIL] TABBY_SYNC_MASTER_KEY_PROVIDER is not set")
		issues++
	} else if provider == "file" {
		if dataDir == "" {
			fmt.Fprintln(stdout, "  [FAIL] cannot check master key file without TABBY_SYNC_DATA_DIR")
			issues++
		} else {
			keyPath := filepath.Join(dataDir, keys.MasterKeyFilename)
			info, err := os.Stat(keyPath)
			if err != nil {
				fmt.Fprintf(stdout, "  [FAIL] master key file does not exist\n")
				issues++
			} else if info.Size() != keys.MasterKeySize {
				fmt.Fprintf(stdout, "  [FAIL] master key file has wrong size (want %d bytes)\n", keys.MasterKeySize)
				issues++
			} else {
				fmt.Fprintln(stdout, "  [OK]   master key file exists and has correct size")
			}
		}
	} else if provider == "env" {
		envKey := getenv(keys.EnvMasterKey)
		if envKey == "" {
			fmt.Fprintln(stdout, "  [FAIL] TABBY_SYNC_MASTER_KEY is not set (provider=env)")
			issues++
		} else if len(envKey) != keys.MasterKeySize*2 {
			fmt.Fprintf(stdout, "  [FAIL] TABBY_SYNC_MASTER_KEY has wrong length (want %d hex chars)\n", keys.MasterKeySize*2)
			issues++
		} else {
			fmt.Fprintln(stdout, "  [OK]   master key env var is set with correct length")
		}
	} else {
		fmt.Fprintf(stdout, "  [FAIL] unknown TABBY_SYNC_MASTER_KEY_PROVIDER: %s (want file|env)\n", provider)
		issues++
	}

	// Check SQLite database.
	if dataDir != "" {
		dbPath := filepath.Join(dataDir, "tabby-sync.db")
		st, err := sqlite.Open(context.Background(), dbPath)
		if err != nil {
			fmt.Fprintf(stdout, "  [FAIL] database cannot be opened: %v\n", scrubPaths(err.Error(), dataDir, dbPath))
			issues++
		} else {
			_ = st.Close()
			fmt.Fprintln(stdout, "  [OK]   database opened successfully (schema up to date)")
		}
	}

	fmt.Fprintln(stdout, "")
	if issues == 0 {
		fmt.Fprintln(stdout, "All checks passed.")
		return 0
	}
	fmt.Fprintf(stdout, "%d issue(s) found.\n", issues)
	return 1
}

// --- helpers ---

func resolveUsersFile(getenv func(string) string) string {
	if f := getenv("TABBY_SYNC_USERS_FILE"); f != "" {
		return f
	}
	if d := getenv("TABBY_SYNC_DATA_DIR"); d != "" {
		return filepath.Join(d, "users.yml")
	}
	return ""
}

func loadUsersYAML(path string) (*usersFileSchema, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- reading the operator-selected users file is the purpose of this CLI command.
	if err != nil {
		return nil, fmt.Errorf("cannot read users file: %w", err)
	}
	var schema usersFileSchema
	if err := yaml.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("cannot parse users file: %w", err)
	}
	return &schema, nil
}

func saveUsersYAML(path string, schema *usersFileSchema) error {
	data, err := yaml.Marshal(schema)
	if err != nil {
		return fmt.Errorf("cannot marshal users file: %w", err)
	}
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("cannot write users file: %w", err)
	}
	return nil
}

// writeFileAtomic writes data to a uniquely named file in the destination
// directory, applies the final mode before any bytes are written, fsyncs the
// file, and renames it into place. CreateTemp starts at 0600; the explicit
// Chmod makes the requested contract visible and keeps this helper correct if
// it is reused for a stricter mode later.
func writeFileAtomic(path string, data []byte, mode os.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmp != nil {
			_ = tmp.Close()
		}
		if retErr != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	tmp = nil
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}

func findUser(schema *usersFileSchema, target string) int {
	// Try as numeric ID first.
	if id, err := strconv.ParseInt(target, 10, 64); err == nil {
		for i, u := range schema.Users {
			if u.ID == id {
				return i
			}
		}
	}
	// Then by name (case-insensitive).
	for i, u := range schema.Users {
		if strings.EqualFold(u.Name, target) {
			return i
		}
	}
	return -1
}

func generateToken() (plaintext, hash, prefix string) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	plaintext = tokenPrefixStr + hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(sum[:])
	prefix = plaintext[:12] // "tbs_" + first 8 hex chars
	return
}
