// Package paseto provides PASETO v4 token creation and verification
// for AurelioMod service-to-service authentication.
//
// PASETO v4 uses Ed25519 (asymmetric, signing) or XChaCha20 (symmetric, encryption).
// This replaces JWT — no alg=none, no algorithm confusion, no CVEs.
package paseto

import (
	"fmt"
	"time"

	pasetolib "aidanwoods.dev/go-paseto"
)

// TokenManager handles PASETO v4 token operations.
type TokenManager struct {
	secretKey pasetolib.V4AsymmetricSecretKey
	publicKey pasetolib.V4AsymmetricPublicKey
	parser    pasetolib.Parser
}

// New creates a TokenManager with a new Ed25519 keypair.
func New() (*TokenManager, error) {
	secretKey := pasetolib.NewV4AsymmetricSecretKey()
	publicKey := secretKey.Public()

	parser := pasetolib.NewParser()
	parser.AddRule(pasetolib.NotExpired())

	return &TokenManager{
		secretKey: secretKey,
		publicKey: publicKey,
		parser:    parser,
	}, nil
}

// NewFromHex creates a TokenManager from an existing hex-encoded secret key.
func NewFromHex(secretKeyHex string) (*TokenManager, error) {
	secretKey, err := pasetolib.NewV4AsymmetricSecretKeyFromHex(secretKeyHex)
	if err != nil {
		return nil, fmt.Errorf("parse secret key: %w", err)
	}
	publicKey := secretKey.Public()

	parser := pasetolib.NewParser()
	parser.AddRule(pasetolib.NotExpired())

	return &TokenManager{
		secretKey: secretKey,
		publicKey: publicKey,
		parser:    parser,
	}, nil
}

// ServiceToken generates a PASETO v4 signed token for service-to-service auth.
func (tm *TokenManager) ServiceToken(serviceName string, ttl time.Duration) (string, error) {
	token := pasetolib.NewToken()
	token.SetIssuer("aureliomod")
	token.SetSubject(serviceName)
	token.SetIssuedAt(time.Now())
	token.SetNotBefore(time.Now())
	token.SetExpiration(time.Now().Add(ttl))
	token.SetString("role", "service")

	return token.V4Sign(tm.secretKey, nil), nil
}

// VerifyToken verifies a PASETO v4 public token and returns claims.
func (tm *TokenManager) VerifyToken(signed string) (*pasetolib.Token, error) {
	token, err := tm.parser.ParseV4Public(tm.publicKey, signed, nil)
	if err != nil {
		return nil, fmt.Errorf("verify paseto token: %w", err)
	}
	return token, nil
}

// PublicKeyHex returns the hex-encoded public key for distribution.
func (tm *TokenManager) PublicKeyHex() string {
	return tm.publicKey.ExportHex()
}

// SecretKeyHex returns the hex-encoded secret key (KEEP SECURE).
func (tm *TokenManager) SecretKeyHex() string {
	return tm.secretKey.ExportHex()
}
