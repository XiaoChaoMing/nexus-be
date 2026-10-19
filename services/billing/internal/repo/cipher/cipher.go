// Package cipher seals payout bank accounts with AES-256-GCM (BILLING_PAYOUT_ENC_KEY).
package cipher

import (
	"crypto/aes"
	gcm "crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
)

const (
	_keyLen = 32
	// _version is the first byte of a sealed blob; it leaves room for a key rotation.
	_version byte = 0x01
)

// ErrKey: the key is not 32 bytes of base64.
var ErrKey = errors.New("BILLING_PAYOUT_ENC_KEY must be 32 bytes, base64")

// ErrBlob: the blob is too short, of another version, or fails authentication.
var ErrBlob = errors.New("cipher: cannot open blob")

// AESGCM implements repo.Cipher: blob = 0x01 ‖ nonce(12) ‖ ciphertext+tag.
type AESGCM struct {
	aead gcm.AEAD
}

var _ repo.Cipher = (*AESGCM)(nil)

// New decodes a base64 key of exactly 32 bytes.
func New(keyB64 string) (*AESGCM, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != _keyLen {
		return nil, ErrKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cipher: %w", err)
	}

	aead, err := gcm.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher: %w", err)
	}

	return &AESGCM{aead: aead}, nil
}

// Seal -.
func (c *AESGCM) Seal(plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("cipher: nonce: %w", err)
	}

	out := append([]byte{_version}, nonce...)

	return c.aead.Seal(out, nonce, plain, aad), nil
}

// Open -.
func (c *AESGCM) Open(blob, aad []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(blob) < 1+n+c.aead.Overhead() || blob[0] != _version {
		return nil, ErrBlob
	}

	plain, err := c.aead.Open(nil, blob[1:1+n], blob[1+n:], aad)
	if err != nil {
		return nil, ErrBlob
	}

	return plain, nil
}
