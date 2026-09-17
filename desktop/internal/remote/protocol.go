package remote

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"

	"github.com/google/uuid"
)

const (
	wireMajor      = 1
	wireMinor      = 0
	messageIDBytes = 16
)

func authSignatureInput(connectionID, challenge []byte, expiresAtMS int64, capabilityHash []byte) []byte {
	result := append([]byte(nil), []byte("waw-relay-device-auth-v1\x00")...)
	result = binary.BigEndian.AppendUint32(result, wireMajor)
	result = appendField(result, connectionID)
	result = appendField(result, challenge)
	result = binary.BigEndian.AppendUint64(result, uint64(expiresAtMS))
	return appendField(result, capabilityHash)
}

func pairGrantSignatureInput(pairingID, daemonID, mobileID, mobileEd25519, mobileX25519 []byte, scopes []string, expiresAtMS int64) []byte {
	ordered := append([]string(nil), scopes...)
	sort.Strings(ordered)
	result := append([]byte(nil), []byte("waw-relay-pair-grant-v1\x00")...)
	result = binary.BigEndian.AppendUint32(result, wireMajor)
	for _, field := range [][]byte{pairingID, daemonID, mobileID, mobileEd25519, mobileX25519} {
		result = appendField(result, field)
	}
	result = binary.BigEndian.AppendUint32(result, uint32(len(ordered)))
	for _, scope := range ordered {
		result = appendField(result, []byte(scope))
	}
	return binary.BigEndian.AppendUint64(result, uint64(expiresAtMS))
}

func pairProofInput(pairingID, challenge, daemonID, daemonEd25519, daemonX25519, mobileEd25519, mobileX25519, daemonCapability, mobileCapability []byte, expiresAtMS int64) []byte {
	result := append([]byte(nil), []byte("waw-relay-pair-proof-v1\x00")...)
	result = binary.BigEndian.AppendUint32(result, wireMajor)
	for _, field := range [][]byte{pairingID, challenge, daemonID, daemonEd25519, daemonX25519, mobileEd25519, mobileX25519, daemonCapability, mobileCapability} {
		result = appendField(result, field)
	}
	return binary.BigEndian.AppendUint64(result, uint64(expiresAtMS))
}

func verifyPairProof(secret, proof, input []byte) bool {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(input)
	return hmac.Equal(mac.Sum(nil), proof)
}

func appendField(target, value []byte) []byte {
	target = binary.BigEndian.AppendUint32(target, uint32(len(value)))
	return append(target, value...)
}

type QRPayload struct {
	RelayURL       string `json:"relayUrl"`
	PairingID      string `json:"pairingId"`
	Secret         string `json:"secret"`
	DaemonDeviceID string `json:"daemonDeviceId"`
	DaemonEd25519  string `json:"daemonEd25519"`
	DaemonX25519   string `json:"daemonX25519"`
	CapabilityHash string `json:"capabilityHash"`
	ComputerName   string `json:"computerName,omitempty"`
	ExpiresAtMS    int64  `json:"expiresAtMs"`
}

func (q QRPayload) URI() (string, error) {
	if _, err := uuid.Parse(q.PairingID); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(q)
	if err != nil {
		return "", err
	}
	return "waw://pair?v=1&payload=" + url.QueryEscape(base64.RawURLEncoding.EncodeToString(encoded)), nil
}

func ParseQR(uri string) (QRPayload, []byte, error) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "waw" || parsed.Host != "pair" || parsed.Query().Get("v") != strconv.Itoa(wireMajor) {
		return QRPayload{}, nil, errors.New("invalid Work Anywhere pairing URI")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parsed.Query().Get("payload"))
	if err != nil {
		return QRPayload{}, nil, err
	}
	var payload QRPayload
	if err = json.Unmarshal(raw, &payload); err != nil {
		return QRPayload{}, nil, err
	}
	secret, err := base64.RawURLEncoding.DecodeString(payload.Secret)
	if err != nil || len(secret) != 32 {
		return QRPayload{}, nil, errors.New("invalid pairing secret")
	}
	return payload, secret, nil
}
