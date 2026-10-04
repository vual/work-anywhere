package store

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vual/work-anywhere/relay/migrations"
)

func enrollmentDatabase(t *testing.T, migrateIdentity bool) *Store {
	t.Helper()
	databaseURL := os.Getenv("RELAY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("RELAY_TEST_DATABASE_URL is required for PostgreSQL integration")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "enrollment_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	initial, err := migrations.Files.ReadFile("00001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, strings.Split(string(initial), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	if migrateIdentity {
		applyIdentityMigration(t, pool)
	}
	return &Store{Pool: pool}
}

func applyIdentityMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	raw, err := migrations.Files.ReadFile("00002_daemon_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
}

func enrollmentInput(t *testing.T) EnrollmentInput {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return EnrollmentInput{Role: 1, DisplayName: "Desk", Ed25519PublicKey: publicKey, X25519PublicKey: bytes.Repeat([]byte{3}, 32), CapabilityHash: bytes.Repeat([]byte{4}, 32)}
}

func TestRegisterDaemonConcurrentRecoveryAndRevocation(t *testing.T) {
	state := enrollmentDatabase(t, true)
	input := enrollmentInput(t)
	ctx := context.Background()
	results := make(chan Device, 12)
	failures := make(chan error, 12)
	var wait sync.WaitGroup
	for index := 0; index < 12; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			device, _, err := state.RegisterDaemon(ctx, input, "self_service", 1, true)
			if err != nil {
				failures <- err
			} else {
				results <- device
			}
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var deviceID uuid.UUID
	for device := range results {
		if deviceID == uuid.Nil {
			deviceID = device.ID
		} else if device.ID != deviceID {
			t.Fatal("duplicate identity")
		}
	}
	if _, created, err := state.RegisterDaemon(ctx, input, "disabled", 1, true); err != nil || created {
		t.Fatalf("recovery: %v created=%v", err, created)
	}
	input.DisplayName = "Renamed"
	device, _, err := state.RegisterDaemon(ctx, input, "invite", 1, true)
	if err != nil || device.DisplayName != "Desk" {
		t.Fatal("recovery changed metadata")
	}
	conflict := input
	conflict.X25519PublicKey = bytes.Repeat([]byte{9}, 32)
	if _, _, err := state.RegisterDaemon(ctx, conflict, "self_service", 1, true); err != EnrollmentError("identity_conflict") {
		t.Fatalf("binding conflict: %v", err)
	}
	if _, _, err := state.RegisterDaemon(ctx, enrollmentInput(t), "self_service", 1, true); err != EnrollmentError("daemon_capacity_reached") {
		t.Fatalf("capacity: %v", err)
	}
	if _, err := state.Pool.Exec(ctx, `UPDATE devices SET status='revoked' WHERE id=$1`, deviceID); err != nil {
		t.Fatal(err)
	}
	for _, recoverIdentity := range []bool{false, true} {
		if _, _, err := state.RegisterDaemon(ctx, input, "open", 2, recoverIdentity); err != EnrollmentError("device_revoked") {
			t.Fatalf("revoked: %v", err)
		}
	}
}

func TestEnrollmentInviteTransactionAndCapacityRollback(t *testing.T) {
	state := enrollmentDatabase(t, true)
	ctx := context.Background()
	input := enrollmentInput(t)
	if _, _, err := state.RegisterDaemon(ctx, input, "invite", 1, true); err != EnrollmentError("invite_required") {
		t.Fatal(err)
	}
	token, hash, err := NewEnrollmentToken()
	if err != nil {
		t.Fatal(err)
	}
	if err = state.CreateEnrollmentToken(ctx, hash, time.Now().Add(time.Hour), 1); err != nil {
		t.Fatal(err)
	}
	input.Token = token
	if _, _, err = state.RegisterDaemon(ctx, input, "invite", 1, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err = state.RegisterDaemon(ctx, input, "invite", 1, true); err != nil {
		t.Fatal("recovery consumed spent invite", err)
	}
	second := enrollmentInput(t)
	second.Token = token
	if _, _, err = state.RegisterDaemon(ctx, second, "invite", 2, true); err != EnrollmentError("invalid_invite") {
		t.Fatal("spent invite reused", err)
	}
	plain, hash, err := NewEnrollmentToken()
	if err != nil {
		t.Fatal(err)
	}
	if err = state.CreateEnrollmentToken(ctx, hash, time.Now().Add(time.Hour), 1); err != nil {
		t.Fatal(err)
	}
	second.Token = plain
	if _, _, err = state.RegisterDaemon(ctx, second, "invite", 1, true); err != EnrollmentError("daemon_capacity_reached") {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(plain))
	var uses int
	if err = state.Pool.QueryRow(ctx, `SELECT remaining_uses FROM enrollment_tokens WHERE token_hash=$1`, digest[:]).Scan(&uses); err != nil || uses != 1 {
		t.Fatal("capacity rejection consumed invite", err)
	}
}

func TestIdentityMigrationRejectsDuplicateDaemons(t *testing.T) {
	state := enrollmentDatabase(t, false)
	ctx := context.Background()
	input := enrollmentInput(t)
	for index := 0; index < 2; index++ {
		if _, err := state.Pool.Exec(ctx, `INSERT INTO devices(id,role,ed25519_public_key,x25519_public_key,capability_hash,status) VALUES($1,1,$2,$3,$4,'active')`, uuid.New(), input.Ed25519PublicKey, input.X25519PublicKey, input.CapabilityHash); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := migrations.Files.ReadFile("00002_daemon_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = state.Pool.Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0]); err == nil || !strings.Contains(err.Error(), "Duplicate daemon identities") {
		t.Fatalf("duplicate migration: %v", err)
	}
	var count int
	if err = state.Pool.QueryRow(ctx, `SELECT count(*) FROM devices`).Scan(&count); err != nil || count != 2 {
		t.Fatal("migration removed devices", err)
	}
}
