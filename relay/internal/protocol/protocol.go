package protocol

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"
)

const (
	WireMajor       = 1
	WireMinor       = 0
	MaxFrameBytes   = 2 << 20
	MaxPayloadBytes = 1 << 20
	MessageIDBytes  = 16
)

func NewID() ([]byte, error) {
	value := make([]byte, MessageIDBytes)
	if _, err := rand.Read(value); err != nil {
		return nil, err
	}
	return value, nil
}

func PairProofInput(pairingID, challenge, daemonID, daemonEd25519, daemonX25519, mobileEd25519, mobileX25519, daemonCapability, mobileCapability []byte, expiresAtMS int64) []byte {
	const domain = "waw-relay-pair-proof-v1\x00"
	result := append([]byte(nil), domain...)
	result = binary.BigEndian.AppendUint32(result, WireMajor)
	for _, field := range [][]byte{pairingID, challenge, daemonID, daemonEd25519, daemonX25519, mobileEd25519, mobileX25519, daemonCapability, mobileCapability} {
		result = appendField(result, field)
	}
	return binary.BigEndian.AppendUint64(result, uint64(expiresAtMS))
}

func PairProof(secret, input []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(input)
	return mac.Sum(nil)
}

func PairGrantSignatureInput(pairingID, daemonID, mobileID, mobileEd25519, mobileX25519 []byte, scopes []string, expiresAtMS int64) []byte {
	const domain = "waw-relay-pair-grant-v1\x00"
	orderedScopes := append([]string(nil), scopes...)
	sort.Strings(orderedScopes)
	result := append([]byte(nil), domain...)
	result = binary.BigEndian.AppendUint32(result, WireMajor)
	for _, field := range [][]byte{pairingID, daemonID, mobileID, mobileEd25519, mobileX25519} {
		result = appendField(result, field)
	}
	result = binary.BigEndian.AppendUint32(result, uint32(len(orderedScopes)))
	for _, scope := range orderedScopes {
		result = appendField(result, []byte(scope))
	}
	return binary.BigEndian.AppendUint64(result, uint64(expiresAtMS))
}

func ValidateID(value []byte) error {
	if len(value) != MessageIDBytes {
		return errors.New("identifier must be 16 bytes")
	}
	return nil
}

func AuthSignatureInput(connectionID, challenge []byte, expiresAtMS int64, capabilityHash []byte) []byte {
	const domain = "waw-relay-device-auth-v1\x00"
	result := make([]byte, 0, len(domain)+4+4+len(connectionID)+4+len(challenge)+8+4+len(capabilityHash))
	result = append(result, domain...)
	result = binary.BigEndian.AppendUint32(result, WireMajor)
	result = appendField(result, connectionID)
	result = appendField(result, challenge)
	result = binary.BigEndian.AppendUint64(result, uint64(expiresAtMS))
	result = appendField(result, capabilityHash)
	return result
}

func appendField(dst, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}
