//go:build !darwin || !cgo

package auth

// NewStore fails closed on any build that cannot reach the macOS Keychain.
// The uploader must never fall back to a plaintext credential file, so this
// build exists only to keep cross-platform tests compiling.
func NewStore() (Store, error) {
	return nil, ErrUnsupportedPlatform
}
