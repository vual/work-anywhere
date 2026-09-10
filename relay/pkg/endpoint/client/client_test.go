package client

import (
	"context"
	"testing"
)

func TestRestoreRejectsIncompleteCredentials(t *testing.T) {
	client := New()
	if err := client.Restore(context.Background(), []byte(`{"pairingId":"not-a-uuid"}`)); err == nil {
		t.Fatal("incomplete credentials were accepted")
	}
}
