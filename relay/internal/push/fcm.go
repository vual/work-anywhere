package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type FCM struct {
	projectID string
	client    *http.Client
}

func NewFCM(serviceAccountJSON []byte, client *http.Client) (*FCM, error) {
	var metadata struct {
		ProjectID string `json:"project_id"`
	}
	if json.Unmarshal(serviceAccountJSON, &metadata) != nil || metadata.ProjectID == "" {
		return nil, errors.New("FCM service account is missing project_id")
	}
	if client == nil {
		credentials, err := google.CredentialsFromJSON(context.Background(), serviceAccountJSON, "https://www.googleapis.com/auth/firebase.messaging")
		if err != nil {
			return nil, fmt.Errorf("parse FCM credentials: %w", err)
		}
		client = oauth2.NewClient(context.Background(), credentials.TokenSource)
		client.Timeout = 15 * time.Second
	}
	return &FCM{projectID: metadata.ProjectID, client: client}, nil
}

func (f *FCM) Send(ctx context.Context, endpoint Endpoint, notification Notification) error {
	requestBody := map[string]any{"message": map[string]any{"token": endpoint.Token, "notification": map[string]string{"title": "Work Anywhere", "body": notificationText(notification.Hint)}, "data": map[string]string{"notificationId": notification.ID}, "android": map[string]any{"collapse_key": notification.CollapseKey, "priority": "HIGH"}}}
	body, _ := json.Marshal(requestBody)
	url := "https://fcm.googleapis.com/v1/projects/" + f.projectID + "/messages:send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	failure, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode == http.StatusNotFound || bytes.Contains(failure, []byte("UNREGISTERED")) {
		return &PermanentError{InvalidToken: true, Reason: "FCM rejected registration token"}
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
		return &PermanentError{Reason: fmt.Sprintf("FCM rejected notification: status %d", response.StatusCode)}
	}
	return fmt.Errorf("FCM temporary response %d", response.StatusCode)
}
