package keys

import (
	"encoding/hex"
	"errors"
	"os"
)

// EnvProvider reads a hex-encoded 32-byte master key from a
// caller-supplied environment lookup keyed by [EnvMasterKey]. The
// expected on-disk format is exactly 64 lowercase or uppercase
// hexadecimal characters; mixed case is accepted and decoded
// case-insensitively.
//
// The provider does NOT echo the env-var value in any error string:
// neither the raw string, its hex-decoded bytes, nor any prefix
// thereof appears in the returned errors.
type EnvProvider struct {
	getenv func(string) string
}

// NewEnvProvider returns an [EnvProvider] that consults getenv for
// the [EnvMasterKey] variable. Passing a nil getenv selects
// [os.Getenv] at Load time so the cli layer can invoke
// [LoadFromConfig] without threading getenv all the way down.
// Tests pass a closure over a map fixture to avoid mutating
// process state.
func NewEnvProvider(getenv func(string) string) *EnvProvider {
	return &EnvProvider{getenv: getenv}
}

// Load reads the configured environment variable, decodes the value
// as hex, and returns the resulting bytes if they are exactly
// [MasterKeySize] long.
//
// Failure modes:
//   - empty value returns [ErrMissing]
//   - non-hex value returns a generic decode error that does NOT
//     echo the offending value
//   - decoded length != [MasterKeySize] returns [ErrInvalidLength]
func (p *EnvProvider) Load() ([]byte, error) {
	get := p.getenv
	if get == nil {
		get = os.Getenv
	}
	raw := get(EnvMasterKey)
	if raw == "" {
		return nil, ErrMissing
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		// Do NOT wrap err: the standard library's hex error messages
		// echo the offending byte (e.g. `encoding/hex: invalid byte:
		// U+0058 'X'`). Returning a generic sentinel keeps the env
		// value out of logs.
		return nil, errors.New("keys: master key env value is not valid hex")
	}
	if len(decoded) != MasterKeySize {
		return nil, ErrInvalidLength
	}
	out := make([]byte, MasterKeySize)
	copy(out, decoded)
	return out, nil
}
