package push

import (
	"context"
	"errors"
)

type Endpoint struct {
	Platform, AppID, Environment, Locale, Token string
}

type Notification struct {
	ID, Hint, CollapseKey string
}

type Sender interface {
	Send(context.Context, Endpoint, Notification) error
}

type PermanentError struct {
	InvalidToken bool
	Reason       string
}

func (e *PermanentError) Error() string { return e.Reason }

func IsInvalidToken(err error) bool {
	var permanent *PermanentError
	return errors.As(err, &permanent) && permanent.InvalidToken
}

type Multi struct {
	APNs Sender
	FCM  Sender
}

func (m Multi) Send(ctx context.Context, endpoint Endpoint, notification Notification) error {
	switch endpoint.Platform {
	case "apns":
		if m.APNs == nil {
			return &PermanentError{Reason: "APNs provider is not configured"}
		}
		return m.APNs.Send(ctx, endpoint, notification)
	case "fcm":
		if m.FCM == nil {
			return &PermanentError{Reason: "FCM provider is not configured"}
		}
		return m.FCM.Send(ctx, endpoint, notification)
	default:
		return &PermanentError{Reason: "unsupported push platform"}
	}
}
