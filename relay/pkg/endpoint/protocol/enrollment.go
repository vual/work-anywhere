package protocol

import "encoding/binary"

const EnrollmentProtocol = "daemon-enrollment-v1"

func EnrollmentSignatureInput(origin string, requestID, challenge []byte, expiresAtMS int64, edPublic, noisePublic, capability []byte, displayName string) []byte {
	result := []byte("waw-relay-daemon-enrollment-v1\x00")
	for _, value := range [][]byte{[]byte(origin), requestID, challenge} {
		result = binary.BigEndian.AppendUint32(result, uint32(len(value)))
		result = append(result, value...)
	}
	result = binary.BigEndian.AppendUint64(result, uint64(expiresAtMS))
	for _, value := range [][]byte{edPublic, noisePublic, capability, []byte(displayName)} {
		result = binary.BigEndian.AppendUint32(result, uint32(len(value)))
		result = append(result, value...)
	}
	return result
}
