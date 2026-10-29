package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/vual/work-anywhere/relay/internal/push"
	"github.com/vual/work-anywhere/relay/internal/secretbox"
	"github.com/vual/work-anywhere/relay/internal/store"
)

const pushQueue = "push"

type Notification struct {
	ID          string
	Hint        string
	CollapseKey string
	ExpiresAt   time.Time
}

type NotificationArgs struct {
	EndpointID     string `json:"endpoint_id" river:"unique"`
	NotificationID string `json:"notification_id" river:"unique"`
	Hint           string `json:"hint"`
	CollapseKey    string `json:"collapse_key"`
	ExpiresAtMS    int64  `json:"expires_at_ms"`
}

func (NotificationArgs) Kind() string { return "push_notification" }

func (NotificationArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: pushQueue, MaxAttempts: 6, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 24 * time.Hour}}
}

type endpointStore interface {
	PushEndpoint(context.Context, uuid.UUID) (store.PushEndpoint, error)
	MarkPushEndpointInvalid(context.Context, uuid.UUID) error
}

type pushWorker struct {
	river.WorkerDefaults[NotificationArgs]
	store  endpointStore
	box    *secretbox.Box
	sender push.Sender
	logger *slog.Logger
}

func (w *pushWorker) Work(ctx context.Context, job *river.Job[NotificationArgs]) error {
	if job.Args.ExpiresAtMS <= time.Now().UnixMilli() {
		return nil
	}
	endpointID, err := uuid.Parse(job.Args.EndpointID)
	if err != nil {
		return nil
	}
	endpoint, err := w.store.PushEndpoint(ctx, endpointID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.New("push endpoint lookup temporarily unavailable")
	}
	if endpoint.Status != "active" {
		return nil
	}
	aad := secretbox.AssociatedData(endpoint.MobileDeviceID.String(), endpoint.Platform, endpoint.AppID, endpoint.Environment)
	token, err := w.box.Open(endpoint.TokenCiphertext, aad)
	if err != nil {
		return fmt.Errorf("decrypt push endpoint: %w", err)
	}
	err = w.sender.Send(ctx, push.Endpoint{Platform: endpoint.Platform, AppID: endpoint.AppID, Environment: endpoint.Environment, Locale: endpoint.Locale, Token: string(token)}, push.Notification{ID: job.Args.NotificationID, Hint: job.Args.Hint, CollapseKey: job.Args.CollapseKey})
	for index := range token {
		token[index] = 0
	}
	if err == nil {
		return nil
	}
	var permanent *push.PermanentError
	if errors.As(err, &permanent) {
		if permanent.InvalidToken {
			_ = w.store.MarkPushEndpointInvalid(ctx, endpointID)
		}
		w.logger.Warn("push.permanent_failure", "endpoint_id", endpointID, "platform", endpoint.Platform, "invalid_token", permanent.InvalidToken)
		return nil
	}
	return err
}

type Manager struct {
	client *river.Client[pgx.Tx]
}

func New(pool *pgxpool.Pool, state endpointStore, box *secretbox.Box, sender push.Sender, logger *slog.Logger) (*Manager, error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &pushWorker{store: state, box: box, sender: sender, logger: logger})
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Queues: map[string]river.QueueConfig{pushQueue: {MaxWorkers: 20}}, Workers: workers})
	if err != nil {
		return nil, err
	}
	return &Manager{client: client}, nil
}

func (m *Manager) Start(ctx context.Context) error { return m.client.Start(ctx) }

func (m *Manager) Stop(ctx context.Context) error { return m.client.StopAndCancel(ctx) }

func (m *Manager) Enqueue(ctx context.Context, endpointIDs []uuid.UUID, notification Notification) error {
	if len(endpointIDs) == 0 {
		return errors.New("mobile has no active push endpoint")
	}
	params := make([]river.InsertManyParams, 0, len(endpointIDs))
	for _, endpointID := range endpointIDs {
		params = append(params, river.InsertManyParams{Args: NotificationArgs{EndpointID: endpointID.String(), NotificationID: notification.ID, Hint: notification.Hint, CollapseKey: notification.CollapseKey, ExpiresAtMS: notification.ExpiresAt.UnixMilli()}})
	}
	_, err := m.client.InsertMany(ctx, params)
	return err
}
