// Package cli implements the tabby-sync command-line dispatcher. It is the
// only place outside of cmd/tabby-sync/main.go that knows how to translate
// argv into a process exit code, and it is the only place that wires
// configuration loading, signal handling, structured logging, and the HTTP
// server lifecycle together.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/kuddy-ai/tabby-sync/internal/api"
	"github.com/kuddy-ai/tabby-sync/internal/auth"
	"github.com/kuddy-ai/tabby-sync/internal/config"
	"github.com/kuddy-ai/tabby-sync/internal/keys"
	"github.com/kuddy-ai/tabby-sync/internal/ratelimit"
	"github.com/kuddy-ai/tabby-sync/internal/server"
	"github.com/kuddy-ai/tabby-sync/internal/store/encrypted"
	"github.com/kuddy-ai/tabby-sync/internal/store/sqlite"
	"github.com/kuddy-ai/tabby-sync/internal/version"
)

const usage = `Usage: tabby-sync <command>

Commands:
  serve     Start the tabby-sync HTTP server
  init      Initialize data directory, master key, and users file
  user      Manage users (add, rm, rotate)
  doctor    Check environment for common configuration problems
  version   Print version information and exit
  help      Show this message

Environment variables (see docs/):
  TABBY_SYNC_ADDR                 Listen address (default :8080)
  TABBY_SYNC_DATA_DIR             Required: data directory
  TABBY_SYNC_USERS_FILE           Required: users credentials file
  TABBY_SYNC_MASTER_KEY_PROVIDER  Required: one of env|file
  APP_LOG_LEVEL                   error|warn|info|debug (default info)
`

// Run dispatches the tabby-sync subcommand named by args and returns a
// process exit code. args is expected to be the full os.Args slice (i.e.
// args[0] is the program name). The getenv callback supplies environment
// values to config.Load so tests can inject a fake env.
//
// Run does not call os.Exit; cmd/tabby-sync/main.go does.
func Run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if getenv == nil {
		getenv = os.Getenv
	}

	cmd := ""
	if len(args) >= 2 {
		cmd = args[1]
	}

	switch cmd {
	case "", "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version":
		fmt.Fprintln(stdout, version.Info())
		return 0
	case "serve":
		return runServe(ctx, getenv, stderr)
	case "init":
		return runInit(args, getenv, stdout, stderr)
	case "user":
		return runUser(args, getenv, stdout, stderr)
	case "doctor":
		return runDoctor(args, getenv, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", cmd)
		fmt.Fprint(stderr, usage)
		return 2
	}
}

// runServe loads configuration, builds the HTTP server, and blocks until the
// supplied context is canceled or a SIGINT/SIGTERM is received.
func runServe(ctx context.Context, getenv func(string) string, stderr io.Writer) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err.Error())
		return 2
	}

	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))

	// Open the SQLite store BEFORE binding the listener so a corrupt or
	// unwritable data directory fails fast with a clear error and no
	// network resources are reserved on the way out. The DB file lives
	// at ${TABBY_SYNC_DATA_DIR}/tabby-sync.db; the absolute path is NOT
	// logged, only that the data directory is set. See
	// docs/LOGGING_POLICY.md and AGENTS.md §7.
	dbPath := filepath.Join(cfg.DataDir, "tabby-sync.db")
	masterKeyPath := filepath.Join(cfg.DataDir, keys.MasterKeyFilename)
	st, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		// The wrapped error from sqlite.Open commonly contains the
		// absolute DB path (os.PathError messages, ping failures echoing
		// the DSN, etc.). Strip cfg.DataDir, dbPath, and the master.key
		// path out before logging so the structured "data_dir" field
		// stays the only place that summarises that location, and so a
		// configured-but-broken install does not leak its on-disk
		// layout to anyone tailing stderr. See review v1 issue #1 for
		// #6. The master.key path is included in the scrub list as
		// defence-in-depth so a future error from any layer that
		// mentions it stays redacted regardless of order.
		logger.Error("failed to open sqlite store",
			slog.String("data_dir", redactPath(cfg.DataDir)),
			slog.String("err", scrubPaths(err.Error(), dbPath, masterKeyPath, cfg.DataDir)),
		)
		return 1
	}
	logger.Info("sqlite store opened", slog.String("data_dir", redactPath(cfg.DataDir)))

	// Load users.yml and build the Bearer-token middleware BEFORE the
	// listener binds, so a missing or malformed users file fails fast
	// with a redacted error and no network resources are reserved on
	// the way out. The on-disk path is NEVER logged: only the redacted
	// "<set>" marker, the count of loaded users, and the (already
	// scrubbed) wrapped error from auth.LoadUsersFile.
	userStore, err := auth.LoadUsersFile(cfg.UsersFile)
	if err != nil {
		logger.Error("failed to load users file",
			slog.String("users_file", redactPath(cfg.UsersFile)),
			slog.String("err", scrubPaths(err.Error(), cfg.UsersFile, masterKeyPath, cfg.DataDir)),
		)
		_ = st.Close()
		return 1
	}
	logger.Info("users file loaded",
		slog.String("users_file", redactPath(cfg.UsersFile)),
		slog.Int("user_count", userStore.UserCount()),
	)
	authMW := auth.Bearer(userStore, logger)

	// Load the master key BEFORE binding the listener so a missing or
	// malformed key fails fast with a clean exit. The provider is the
	// only structured field on the success log line: no path, no key,
	// no length, per docs/LOGGING_POLICY.md and AGENTS.md §7. On
	// failure the wrapped error is scrubbed of the data dir, master.key
	// path, users file path, and the DB path so the operator only sees
	// the redacted summary and the generic provider name.
	_, masterKey, err := keys.LoadFromConfig(cfg)
	if err != nil {
		logger.Error("failed to load master key",
			slog.String("provider", cfg.MasterKeyProvider),
			slog.String("err", scrubPaths(err.Error(), masterKeyPath, dbPath, cfg.UsersFile, cfg.DataDir)),
		)
		_ = st.Close()
		return 1
	}
	logger.Info("master key loaded", slog.String("provider", cfg.MasterKeyProvider))

	encStore, err := encrypted.New(st, masterKey)
	// Wipe the local copy of the master key now that the wrapper holds
	// its own defensive copy. Best-effort hygiene; see
	// internal/crypto.zero for the limitations.
	for i := range masterKey {
		masterKey[i] = 0
	}
	if err != nil {
		logger.Error("failed to wrap store with encryption",
			slog.String("provider", cfg.MasterKeyProvider),
			slog.String("err", scrubPaths(err.Error(), masterKeyPath, dbPath, cfg.UsersFile, cfg.DataDir)),
		)
		_ = st.Close()
		return 1
	}
	defer func() {
		if cerr := encStore.Close(); cerr != nil {
			logger.Error("failed to close encrypted store", slog.String("err", cerr.Error()))
		}
	}()
	// Construct the API handler around the encrypted-store wrapper
	// and mount it on the server below. The wrapper is the single
	// seam to the cryptographic envelope, so plaintext never leaves
	// this scope on the way into the handlers or back out of them.
	apiHandler := api.New(encStore, logger)

	// Bind the listener up front so we can report the actual port and so we
	// fail fast (with a clear error) before spinning up goroutines.
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		// cfg.Addr is non-sensitive so it is safe to mention in the log.
		logger.Error("failed to bind listener", slog.String("addr", cfg.Addr), slog.String("err", err.Error()))
		return 1
	}

	logger.Info(
		"starting tabby-sync",
		slog.String("version", version.Version),
		slog.String("commit", version.Commit),
		slog.String("addr", cfg.Addr),
		slog.String("config", cfg.String()),
	)

	srv := server.New(cfg, logger, authMW, apiHandler, ratelimit.New(60, time.Minute))

	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("tabby-sync ready", slog.String("listen", ln.Addr().String()))

	if err := server.Run(signalCtx, srv, ln, logger); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger.Warn("tabby-sync shutdown timed out, forcing close", slog.String("err", err.Error()))
			return 1
		}
		logger.Error("tabby-sync exited with error", slog.String("err", err.Error()))
		return 1
	}

	logger.Info("tabby-sync stopped")
	return 0
}

// redactPath mirrors the redact helper in internal/config: a non-empty
// value renders as "<set>" so logs do not leak filesystem paths, while
// an empty value renders as "<unset>". The store layer reuses this so
// startup logs that mention TABBY_SYNC_DATA_DIR stay consistent with
// the redacted summary already emitted by config.Config.String().
func redactPath(v string) string {
	if v == "" {
		return "<unset>"
	}
	return "<set>"
}

// scrubPaths returns msg with every supplied secret replaced by the
// "<redacted>" sentinel. It is used to wash filesystem paths out of
// wrapped error strings before they reach the structured logger: the
// SQLite driver, os.PathError, and net.OpError all happily echo the
// absolute DB path, which would defeat the redactPath() field on the
// same log line. The replacement is a literal substring match so the
// helper does not depend on path-format quirks. Empty secrets are
// skipped to avoid an infinite "<redacted>" sprinkle when DataDir is
// unset (which Load already rejects, but defence in depth is cheap).
//
// Secrets are processed longest-first so a shorter prefix (e.g.
// cfg.DataDir) cannot consume part of a longer secret (e.g. dbPath =
// filepath.Join(cfg.DataDir, "tabby-sync.db")) and leave the basename
// behind in the redacted output. v2 semantic review issue #3 for #6
// flagged this as a defence-in-depth gap; sorting here means the call
// site is no longer order-sensitive and a future maintainer cannot
// reintroduce the leak by reordering arguments.
func scrubPaths(msg string, secrets ...string) string {
	// Copy so we do not mutate the caller's slice.
	ordered := append([]string(nil), secrets...)
	sort.Slice(ordered, func(i, j int) bool {
		return len(ordered[i]) > len(ordered[j])
	})
	for _, s := range ordered {
		if s == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, s, "<redacted>")
	}
	return msg
}

// parseLogLevel maps a config log-level string to a slog.Level. config.Load
// already validates that the string is one of error/warn/info/debug, so the
// default branch is defensive only.
func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "error":
		return slog.LevelError
	case "warn":
		return slog.LevelWarn
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}
