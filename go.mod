module github.com/kuddy-ai/tabby-sync

go 1.25.0

// gopkg.in/check.v1 appears in go.sum but NOT in go.mod's require
// block: it is a test-only transitive of gopkg.in/yaml.v3 (see
// yaml.v3's own go.mod), which the Go toolchain records in go.sum
// for build reproducibility but does not compile into our binary.
// AGENTS.md §4.1 requires human confirmation before adding deps;
// the orchestrator brief for issue #7 authorised yaml.v3 v3.0.1
// only, and check.v1 is unavoidable when adding yaml.v3. It is
// called out here so a future reviewer comparing go.sum against
// the authorised dep list does not flag it as an unsanctioned
// addition. See CHANGELOG.md and the v1 review for #7, issue #8.

require (
	golang.org/x/crypto v0.31.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.50.1
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.42.0 // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
