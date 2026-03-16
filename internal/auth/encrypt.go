package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// EncryptPassword encrypts a plaintext password using AES-GCM.
// The key should be a hex-encoded 16- or 32-byte key (32 or 64 hex chars).
// Returns nonce + ciphertext.
func EncryptPassword(hexKey string, plaintext string) ([]byte, error) {
	key, err := deriveKey(hexKey)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// DecryptPassword decrypts an AES-GCM encrypted password.
func DecryptPassword(hexKey string, ciphertext []byte) (string, error) {
	key, err := deriveKey(hexKey)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// deriveKey converts a hex key string to a 16- or 32-byte AES key.
func deriveKey(hexKey string) ([]byte, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex key: %w", err)
	}
	if len(key) != 16 && len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 or 64 hex chars (16 or 32 bytes), got %d bytes", len(key))
	}
	return key, nil
}
