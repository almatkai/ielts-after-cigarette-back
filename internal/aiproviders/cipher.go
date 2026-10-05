package aiproviders

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

var ErrEncryption = errors.New("AI provider encryption is unavailable or authentication failed")

// Keys never enter PostgreSQL. The previous key permits rolling deployment and
// re-encryption during rotation. AES-GCM authenticates the ID AND destination:
// changing a database row's endpoint cannot exfiltrate its decrypted API key.
type Cipher struct {
	current [8]byte
	keys    map[[8]byte]cipher.AEAD
}

func NewCipher(current, previous string) (*Cipher, error) {
	if current == "" {
		if previous != "" {
			return nil, ErrEncryption
		}
		return nil, nil
	}
	c := &Cipher{keys: make(map[[8]byte]cipher.AEAD)}
	for index, encoded := range []string{current, previous} {
		if encoded == "" {
			continue
		}
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return nil, ErrEncryption
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, ErrEncryption
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, ErrEncryption
		}
		hash := sha256.Sum256(key)
		var id [8]byte
		copy(id[:], hash[:8])
		c.keys[id] = aead
		if index == 0 {
			c.current = id
		}
	}
	return c, nil
}
func (c *Cipher) Seal(key, aad string) ([]byte, error) {
	if c == nil {
		return nil, ErrEncryption
	}
	aead := c.keys[c.current]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrEncryption
	}
	out := append([]byte{1}, c.current[:]...)
	out = append(out, nonce...)
	return aead.Seal(out, nonce, []byte(key), []byte(aad)), nil
}
func (c *Cipher) Open(data []byte, aad string) (string, error) {
	if c == nil || len(data) < 9 || data[0] != 1 {
		return "", ErrEncryption
	}
	var id [8]byte
	copy(id[:], data[1:9])
	aead := c.keys[id]
	if aead == nil || len(data) < 9+aead.NonceSize()+aead.Overhead() {
		return "", ErrEncryption
	}
	plain, err := aead.Open(nil, data[9:9+aead.NonceSize()], data[9+aead.NonceSize():], []byte(aad))
	if err != nil {
		return "", ErrEncryption
	}
	return string(plain), nil
}
