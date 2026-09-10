// Package e2ee is the Relay-internal compatibility surface. Endpoint clients
// should import pkg/endpoint/e2ee directly.
package e2ee

import endpoint "github.com/vual/work-anywhere/relay/pkg/endpoint/e2ee"

const (
	SuiteName          = endpoint.SuiteName
	ControlContentType = endpoint.ControlContentType
	AttachmentType     = endpoint.AttachmentType
)

type Binding = endpoint.Binding
type Session = endpoint.Session

var (
	GenerateStaticKey = endpoint.GenerateStaticKey
	NewHandshake      = endpoint.NewHandshake
	NewSession        = endpoint.NewSession
)
