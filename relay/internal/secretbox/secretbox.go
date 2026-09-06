package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
)

type Box struct{ aead cipher.AEAD }

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("secretbox key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func AssociatedData(parts ...string) []byte {
	out := []byte("work-anywhere/secretbox/v1\x00")
	for _, part := range parts {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(part)))
		out = append(out, size[:]...)
		out = append(out, part...)
	}
	return out
}

func (b *Box) Seal(plain, associatedData []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plain, associatedData), nil
}

func (b *Box) Open(ciphertext, associatedData []byte) ([]byte, error) {
	if len(ciphertext) < b.aead.NonceSize() {
		return nil, errors.New("secretbox ciphertext is truncated")
	}
	nonce := ciphertext[:b.aead.NonceSize()]
	return b.aead.Open(nil, nonce, ciphertext[b.aead.NonceSize():], associatedData)
}
