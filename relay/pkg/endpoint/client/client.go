// Package client exposes the reusable Relay endpoint client used by Mobile.
// Its public location is intentional: endpoint applications must not depend on
// Relay server internals.
package client

import "github.com/vual/work-anywhere/relay/internal/devmobile"

type QRPayload = devmobile.QRPayload
type Client = devmobile.Client
type Status = devmobile.Status
type CredentialSet = devmobile.CredentialSet
type CallError = devmobile.CallError

var New = devmobile.New
