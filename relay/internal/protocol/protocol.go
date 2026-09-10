// Package protocol is the Relay-internal compatibility surface. New endpoint
// consumers must import pkg/endpoint/protocol directly.
package protocol

import endpoint "github.com/vual/work-anywhere/relay/pkg/endpoint/protocol"

const (
	WireMajor       = endpoint.WireMajor
	WireMinor       = endpoint.WireMinor
	MaxFrameBytes   = endpoint.MaxFrameBytes
	MaxPayloadBytes = endpoint.MaxPayloadBytes
	MessageIDBytes  = endpoint.MessageIDBytes
)

var (
	NewID                   = endpoint.NewID
	PairProofInput          = endpoint.PairProofInput
	PairProof               = endpoint.PairProof
	PairGrantSignatureInput = endpoint.PairGrantSignatureInput
	ValidateID              = endpoint.ValidateID
	AuthSignatureInput      = endpoint.AuthSignatureInput
)
