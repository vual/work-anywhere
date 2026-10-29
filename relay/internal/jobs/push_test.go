package jobs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/vual/work-anywhere/relay/internal/push"
	"github.com/vual/work-anywhere/relay/internal/secretbox"
	"github.com/vual/work-anywhere/relay/internal/store"
)

type fakeEndpointStore struct {
	endpoint    store.PushEndpoint
	invalid     bool
	lookupError error
}

func (f *fakeEndpointStore) PushEndpoint(context.Context, uuid.UUID) (store.PushEndpoint, error) {
	return f.endpoint, f.lookupError
}
func (f *fakeEndpointStore) MarkPushEndpointInvalid(context.Context, uuid.UUID) error {
	f.invalid = true
	return nil
}

type invalidSender struct{}

func (invalidSender) Send(context.Context, push.Endpoint, push.Notification) error {
	return &push.PermanentError{InvalidToken: true, Reason: "invalid"}
}

func TestPushWorkerDecryptsAndInvalidatesProviderToken(t *testing.T) {
	box, _ := secretbox.New(bytes.Repeat([]byte{3}, 32))
	mobileID, endpointID := uuid.New(), uuid.New()
	aad := secretbox.AssociatedData(mobileID.String(), "fcm", "app", "test")
	ciphertext, _ := box.Seal([]byte("provider-token"), aad)
	state := &fakeEndpointStore{endpoint: store.PushEndpoint{ID: endpointID, MobileDeviceID: mobileID, Platform: "fcm", AppID: "app", Environment: "test", TokenCiphertext: ciphertext, Status: "active"}}
	worker := &pushWorker{store: state, box: box, sender: invalidSender{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	err := worker.Work(context.Background(), &river.Job[NotificationArgs]{Args: NotificationArgs{EndpointID: endpointID.String(), NotificationID: "notice", Hint: "generic", ExpiresAtMS: time.Now().Add(time.Minute).UnixMilli()}})
	if err != nil || !state.invalid {
		t.Fatalf("invalid endpoint was not disabled: invalid=%v err=%v", state.invalid, err)
	}
}

type temporarySender struct{}

func (temporarySender) Send(context.Context, push.Endpoint, push.Notification) error {
	return errors.New("temporary")
}

func TestPushWorkerRetriesTemporaryFailure(t *testing.T) {
	box, _ := secretbox.New(bytes.Repeat([]byte{4}, 32))
	mobileID, endpointID := uuid.New(), uuid.New()
	aad := secretbox.AssociatedData(mobileID.String(), "apns", "app", "sandbox")
	ciphertext, _ := box.Seal([]byte("provider-token"), aad)
	state := &fakeEndpointStore{endpoint: store.PushEndpoint{ID: endpointID, MobileDeviceID: mobileID, Platform: "apns", AppID: "app", Environment: "sandbox", TokenCiphertext: ciphertext, Status: "active"}}
	worker := &pushWorker{store: state, box: box, sender: temporarySender{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	err := worker.Work(context.Background(), &river.Job[NotificationArgs]{Args: NotificationArgs{EndpointID: endpointID.String(), ExpiresAtMS: time.Now().Add(time.Minute).UnixMilli()}})
	if err == nil || state.invalid {
		t.Fatalf("temporary failure should retry without invalidation: invalid=%v err=%v", state.invalid, err)
	}
}

func TestPushEndpointLookupErrorClassification(t *testing.T) {
	for _, tt := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"missing", pgx.ErrNoRows, false}, {"timeout", context.DeadlineExceeded, true},
		{"connection", errors.New("database unavailable secret detail"), true}, {"disabled", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := &fakeEndpointStore{lookupError: tt.err, endpoint: store.PushEndpoint{Status: "disabled"}}
			worker := &pushWorker{store: state}
			err := worker.Work(context.Background(), &river.Job[NotificationArgs]{Args: NotificationArgs{EndpointID: uuid.NewString(), ExpiresAtMS: time.Now().Add(time.Minute).UnixMilli()}})
			if (err != nil) != tt.retry {
				t.Fatalf("retry=%v err=%v", tt.retry, err)
			}
			if err != nil && err.Error() != "push endpoint lookup temporarily unavailable" {
				t.Fatalf("unstable/private lookup error: %v", err)
			}
		})
	}
}
