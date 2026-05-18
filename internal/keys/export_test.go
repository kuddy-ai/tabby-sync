package keys

// WrapPathErrorForTest exposes the unexported wrapPathError helper so
// the external keys_test package can feed it a synthesized error
// directly. The production code paths that reach wrapPathError are
// thin (one filesystem syscall each), and a real failed os.Rename is
// hard to provoke deterministically, so the rename branch is pinned
// by feeding wrapPathError a synthetic [*os.LinkError] in a unit
// test rather than wrestling the filesystem into reproducing one.
//
// This file is only compiled into the test binary thanks to the
// _test.go suffix; the symbol is not exported in the production
// package.
func WrapPathErrorForTest(op string, err error) error {
	return wrapPathError(op, err)
}
