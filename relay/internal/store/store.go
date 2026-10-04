package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/vual/work-anywhere/relay/migrations"
)

type Store struct {
	Pool *pgxpool.Pool
}

var ErrDeviceNotFound = errors.New("device not found")

type Device struct {
	ID               uuid.UUID
	Role             int16
	DisplayName      string
	Ed25519PublicKey []byte
	X25519PublicKey  []byte
	CapabilityHash   []byte
	Status           string
}

type EnrollmentInput struct {
	Token            string
	Role             int16
	DisplayName      string
	Ed25519PublicKey []byte
	X25519PublicKey  []byte
	CapabilityHash   []byte
}

type PairingSession struct {
	ID             uuid.UUID
	DaemonDeviceID uuid.UUID
	ExpiresAt      time.Time
	AttemptCount   int
	Status         string
}

type MobileEnrollment struct {
	ID               uuid.UUID
	DisplayName      string
	Ed25519PublicKey []byte
	X25519PublicKey  []byte
	CapabilityHash   []byte
}

type PushEndpoint struct {
	ID              uuid.UUID
	MobileDeviceID  uuid.UUID
	Platform        string
	AppID           string
	Environment     string
	Locale          string
	TokenCiphertext []byte
	Generation      int64
	Status          string
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() { s.Pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }

func Migrate(ctx context.Context, databaseURL, command string) error {
	if command == "up" {
		pool, err := pgxpool.New(ctx, databaseURL)
		if err != nil {
			return err
		}
		migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
		if err == nil {
			_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
		}
		pool.Close()
		if err != nil {
			return fmt.Errorf("migrate River schema: %w", err)
		}
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err = db.PingContext(ctx); err != nil {
		return err
	}
	goose.SetBaseFS(migrations.Files)
	if err = goose.SetDialect("postgres"); err != nil {
		return err
	}
	switch command {
	case "up":
		return goose.UpContext(ctx, db, ".")
	case "status":
		return goose.StatusContext(ctx, db, ".")
	default:
		return fmt.Errorf("unsupported migration command %q", command)
	}
}

func NewEnrollmentToken() (plain string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", nil, err
	}
	plain = base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(plain))
	return plain, digest[:], nil
}

func (s *Store) CreateEnrollmentToken(ctx context.Context, tokenHash []byte, expiresAt time.Time, uses int) error {
	if len(tokenHash) != sha256.Size || uses <= 0 {
		return errors.New("invalid enrollment token parameters")
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO enrollment_tokens(id, token_hash, expires_at, remaining_uses) VALUES($1,$2,$3,$4)`, uuid.New(), tokenHash, expiresAt, uses)
	return err
}

func (s *Store) RevokeEnrollmentToken(ctx context.Context, token string) error {
	digest := sha256.Sum256([]byte(token))
	result, err := s.Pool.Exec(ctx, `UPDATE enrollment_tokens SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, digest[:])
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("enrollment token not found or already revoked")
	}
	return nil
}

func (s *Store) Enroll(ctx context.Context, input EnrollmentInput, allowOpen bool) (Device, error) {
	mode := "invite"
	if allowOpen {
		mode = "open"
	}
	device, _, err := s.RegisterDaemon(ctx, input, mode, 10_000, false)
	return device, err
}

func (s *Store) Device(ctx context.Context, id uuid.UUID) (Device, error) {
	var value Device
	err := s.Pool.QueryRow(ctx, `SELECT id,role,display_name,ed25519_public_key,x25519_public_key,capability_hash,status FROM devices WHERE id=$1`, id).
		Scan(&value.ID, &value.Role, &value.DisplayName, &value.Ed25519PublicKey, &value.X25519PublicKey, &value.CapabilityHash, &value.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, ErrDeviceNotFound
	}
	return value, err
}

func (s *Store) TouchDevice(ctx context.Context, id uuid.UUID) {
	_, _ = s.Pool.Exec(ctx, `UPDATE devices SET last_seen_at=now() WHERE id=$1`, id)
}

func (s *Store) ActivePairing(ctx context.Context, sender, recipient uuid.UUID) (uuid.UUID, []string, error) {
	var id uuid.UUID
	var scopes []string
	err := s.Pool.QueryRow(ctx, `SELECT id,scopes FROM pairings WHERE status='active' AND
		((daemon_device_id=$1 AND mobile_device_id=$2) OR (daemon_device_id=$2 AND mobile_device_id=$1))`, sender, recipient).Scan(&id, &scopes)
	return id, scopes, err
}

func (s *Store) RevokePairing(ctx context.Context, pairingID, actor uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	var daemonID, mobileID uuid.UUID
	err := s.Pool.QueryRow(ctx, `UPDATE pairings SET status='revoked',revoked_at=now()
		WHERE id=$1 AND status='active' AND (daemon_device_id=$2 OR mobile_device_id=$2)
		RETURNING daemon_device_id,mobile_device_id`, pairingID, actor).Scan(&daemonID, &mobileID)
	return daemonID, mobileID, err
}

func (s *Store) UpsertPushEndpoint(ctx context.Context, endpoint PushEndpoint) (uuid.UUID, error) {
	if endpoint.ID == uuid.Nil {
		endpoint.ID = uuid.New()
	}
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `INSERT INTO push_endpoints(id,mobile_device_id,platform,app_id,environment,locale,token_ciphertext,key_version,generation,status)
		VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,'active')
		ON CONFLICT(mobile_device_id,platform,app_id,environment) DO UPDATE SET
		locale=excluded.locale,token_ciphertext=excluded.token_ciphertext,key_version=excluded.key_version,
		generation=excluded.generation,status='active',updated_at=now()
		WHERE push_endpoints.generation<=excluded.generation RETURNING id`, endpoint.ID, endpoint.MobileDeviceID, endpoint.Platform, endpoint.AppID, endpoint.Environment, endpoint.Locale, endpoint.TokenCiphertext, endpoint.Generation).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errors.New("push endpoint generation is stale")
	}
	return id, err
}

func (s *Store) DeletePushEndpoint(ctx context.Context, mobileID uuid.UUID, platform, appID, environment string) error {
	result, err := s.Pool.Exec(ctx, `UPDATE push_endpoints SET status='deleted',token_ciphertext=decode('','hex'),updated_at=now()
		WHERE mobile_device_id=$1 AND platform=$2 AND app_id=$3 AND environment=$4 AND status<>'deleted'`, mobileID, platform, appID, environment)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("push endpoint not found")
	}
	return nil
}

func (s *Store) ActivePushEndpointIDs(ctx context.Context, mobileID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM push_endpoints WHERE mobile_device_id=$1 AND status='active'`, mobileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (s *Store) PushEndpoint(ctx context.Context, id uuid.UUID) (PushEndpoint, error) {
	var endpoint PushEndpoint
	err := s.Pool.QueryRow(ctx, `SELECT id,mobile_device_id,platform,app_id,environment,locale,token_ciphertext,generation,status
		FROM push_endpoints WHERE id=$1`, id).Scan(&endpoint.ID, &endpoint.MobileDeviceID, &endpoint.Platform, &endpoint.AppID, &endpoint.Environment, &endpoint.Locale, &endpoint.TokenCiphertext, &endpoint.Generation, &endpoint.Status)
	return endpoint, err
}

func (s *Store) MarkPushEndpointInvalid(ctx context.Context, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE push_endpoints SET status='invalid',token_ciphertext=decode('','hex'),updated_at=now() WHERE id=$1`, id)
	return err
}

func (s *Store) CreatePairingSession(ctx context.Context, pairingID, daemonID uuid.UUID, expiresAt time.Time) error {
	result, err := s.Pool.Exec(ctx, `INSERT INTO pairing_sessions(id,daemon_device_id,expires_at,status)
		SELECT $1,$2,$3,'pending' WHERE EXISTS(SELECT 1 FROM devices WHERE id=$2 AND role=1 AND status='active')`, pairingID, daemonID, expiresAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("active daemon device not found")
	}
	return nil
}

func (s *Store) CancelPairingSession(ctx context.Context, pairingID, daemonID uuid.UUID) error {
	command, err := s.Pool.Exec(ctx, `UPDATE pairing_sessions SET status='cancelled' WHERE id=$1 AND daemon_device_id=$2 AND status='pending'`, pairingID, daemonID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) ClaimPairingAttempt(ctx context.Context, pairingID uuid.UUID) (PairingSession, error) {
	var value PairingSession
	err := s.Pool.QueryRow(ctx, `UPDATE pairing_sessions SET attempt_count=attempt_count+1
		WHERE id=$1 AND status='pending' AND expires_at>now() AND attempt_count<5
		RETURNING id,daemon_device_id,expires_at,attempt_count,status`, pairingID).
		Scan(&value.ID, &value.DaemonDeviceID, &value.ExpiresAt, &value.AttemptCount, &value.Status)
	return value, err
}

func (s *Store) CompletePairing(ctx context.Context, session PairingSession, mobile MobileEnrollment, scopes []string, grantSignature []byte) (uuid.UUID, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var status string
	var expiresAt time.Time
	if err = tx.QueryRow(ctx, `SELECT status,expires_at FROM pairing_sessions WHERE id=$1 AND daemon_device_id=$2 FOR UPDATE`, session.ID, session.DaemonDeviceID).Scan(&status, &expiresAt); err != nil {
		return uuid.Nil, err
	}
	if status != "pending" || time.Now().After(expiresAt) {
		return uuid.Nil, errors.New("pairing session is no longer active")
	}
	if mobile.ID == uuid.Nil {
		mobile.ID = uuid.New()
	}
	_, err = tx.Exec(ctx, `INSERT INTO devices(id,role,display_name,ed25519_public_key,x25519_public_key,capability_hash,status)
		VALUES($1,2,$2,$3,$4,$5,'active')`, mobile.ID, mobile.DisplayName, mobile.Ed25519PublicKey, mobile.X25519PublicKey, mobile.CapabilityHash)
	if err != nil {
		return uuid.Nil, err
	}
	grantHash := sha256.Sum256(grantSignature)
	pairingID := session.ID
	_, err = tx.Exec(ctx, `INSERT INTO pairings(id,daemon_device_id,mobile_device_id,scopes,status,grant_hash)
		VALUES($1,$2,$3,$4,'active',$5)`, pairingID, session.DaemonDeviceID, mobile.ID, scopes, grantHash[:])
	if err != nil {
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE pairing_sessions SET status='approved' WHERE id=$1`, session.ID); err != nil {
		return uuid.Nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return pairingID, nil
}
