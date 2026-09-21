//go:build !darwin || !cgo

package auth

import (
	"errors"
	"testing"
)

func TestNewStoreIsUnsupported(t *testing.T) {
	store, err := NewStore()
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("NewStore error = %v, want ErrUnsupportedPlatform", err)
	}
	if store != nil {
		t.Fatal("NewStore must not return a store on an unsupported build")
	}
}
