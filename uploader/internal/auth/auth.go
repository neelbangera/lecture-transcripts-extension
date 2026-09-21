// Package auth owns GitHub App credential storage and the GitHub App Device
// Flow.  It is the only package allowed to touch credential storage; every
// other package receives at most an in-memory Authorization header value.
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

// Keychain identifiers.  The credential and the in-flight device transaction
// are always separate records.
const (
	KeychainService    = "com.neelbangera.lecturetranscripts"
	CredentialAccount  = "github-app-user-token"
	TransactionAccount = "github-app-device-transaction"
)

var (
	// ErrNoCredential is returned when no credential record exists.
	ErrNoCredential = errors.New("no stored credential")
	// ErrNoTransaction is returned when no device-flow transaction exists.
	ErrNoTransaction = errors.New("no in-flight device transaction")
	// ErrUnsupportedPlatform is returned by the credential store on a build
	// that cannot reach the macOS Keychain.  There is no plaintext fallback.
	ErrUnsupportedPlatform = errors.New("credential storage requires macOS with cgo enabled")
	// ErrNotConnected is returned when an operation needs a credential and
	// none is stored.
	ErrNotConnected = errors.New("github is not connected")
	// ErrReauthorizationRequired is returned when the stored credential is
	// missing, expired, revoked, or cannot be refreshed.
	ErrReauthorizationRequired = errors.New("github reauthorization is required")
	// ErrTargetRepositoryUnavailable is returned when the just-authorized
	// credential cannot access the configured repository.
	ErrTargetRepositoryUnavailable = errors.New("target repository is unavailable")
)

// Credential is the complete GitHub App user credential.  It is stored as one
// replaceable record so a refresh can never leave a partial token pair.
type Credential struct {
	AccessToken           string    `json:"accessToken"`
	RefreshToken          string    `json:"refreshToken,omitempty"`
	AccessTokenExpiresAt  time.Time `json:"accessTokenExpiresAt,omitempty"`
	RefreshTokenExpiresAt time.Time `json:"refreshTokenExpiresAt,omitempty"`
	TokenType             string    `json:"tokenType,omitempty"`
	RepositoryID          int64     `json:"repositoryId"`
	RepositoryFullName    string    `json:"repositoryFullName"`
}

// DeviceTransaction is the resumable in-flight device authorization.  It is
// stored separately from the credential and deleted on success, expiry, or a
// terminal device-flow error.
type DeviceTransaction struct {
	DeviceCode              string        `json:"deviceCode"`
	UserCode                string        `json:"userCode"`
	VerificationURI         string        `json:"verificationUri"`
	VerificationURIComplete string        `json:"verificationUriComplete,omitempty"`
	ExpiresAt               time.Time     `json:"expiresAt"`
	Interval                time.Duration `json:"interval"`
}

// Challenge is the user-facing subset of a device transaction.  It contains no
// token material.
type Challenge struct {
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresAt               time.Time
	Interval                time.Duration
}

// Store is the credential-storage contract.  Implementations must replace the
// credential atomically and must never fall back to a plaintext file.
type Store interface {
	LoadCredential() (*Credential, error)
	SaveCredential(Credential) error
	DeleteCredential() error

	LoadTransaction() (*DeviceTransaction, error)
	SaveTransaction(DeviceTransaction) error
	DeleteTransaction() error
}

// RepositoryVerifier proves that a credential can access the configured
// repository and branch before authorization is reported as connected.
type RepositoryVerifier interface {
	VerifyRepository(ctx context.Context, credential Credential) error
}

// State is an alias for the closed wire auth vocabulary.
type State = protocol.AuthState
