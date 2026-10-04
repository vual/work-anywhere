package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type EnrollmentError string

func (err EnrollmentError) Error() string { return string(err) }

func (s *Store) RegisterDaemon(ctx context.Context, input EnrollmentInput, mode string, maxDaemons int, recoverIdentity bool) (Device, bool, error) {
	if input.Role != 1 || len(input.Ed25519PublicKey) != 32 || len(input.X25519PublicKey) != 32 || len(input.CapabilityHash) != 32 || len(input.DisplayName) > 256 {
		return Device{}, false, EnrollmentError("invalid_request")
	}
	if maxDaemons <= 0 {
		maxDaemons = 10_000
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Device{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(876321901)`); err != nil {
		return Device{}, false, err
	}
	var device Device
	err = tx.QueryRow(ctx, `SELECT id,role,display_name,ed25519_public_key,x25519_public_key,capability_hash,status FROM devices WHERE role=1 AND ed25519_public_key=$1`, input.Ed25519PublicKey).
		Scan(&device.ID, &device.Role, &device.DisplayName, &device.Ed25519PublicKey, &device.X25519PublicKey, &device.CapabilityHash, &device.Status)
	if err == nil {
		if device.Status != "active" {
			return Device{}, false, EnrollmentError("device_revoked")
		}
		if !recoverIdentity || !bytes.Equal(device.X25519PublicKey, input.X25519PublicKey) || !bytes.Equal(device.CapabilityHash, input.CapabilityHash) {
			return Device{}, false, EnrollmentError("identity_conflict")
		}
		return device, false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Device{}, false, err
	}
	switch mode {
	case "invite":
		if input.Token == "" {
			return Device{}, false, EnrollmentError("invite_required")
		}
		digest := sha256.Sum256([]byte(input.Token))
		var tokenID uuid.UUID
		err = tx.QueryRow(ctx, `UPDATE enrollment_tokens SET remaining_uses=remaining_uses-1 WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>now() AND remaining_uses>0 RETURNING id`, digest[:]).Scan(&tokenID)
		if errors.Is(err, pgx.ErrNoRows) {
			return Device{}, false, EnrollmentError("invalid_invite")
		}
		if err != nil {
			return Device{}, false, err
		}
	case "self_service", "open":
	case "disabled":
		return Device{}, false, EnrollmentError("enrollment_disabled")
	default:
		return Device{}, false, EnrollmentError("enrollment_disabled")
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM devices WHERE role=1`).Scan(&count); err != nil {
		return Device{}, false, err
	}
	if count >= maxDaemons {
		return Device{}, false, EnrollmentError("daemon_capacity_reached")
	}
	device = Device{ID: uuid.New(), Role: 1, DisplayName: input.DisplayName, Ed25519PublicKey: input.Ed25519PublicKey, X25519PublicKey: input.X25519PublicKey, CapabilityHash: input.CapabilityHash, Status: "active"}
	_, err = tx.Exec(ctx, `INSERT INTO devices(id,role,display_name,ed25519_public_key,x25519_public_key,capability_hash,status) VALUES($1,$2,$3,$4,$5,$6,'active')`, device.ID, device.Role, device.DisplayName, device.Ed25519PublicKey, device.X25519PublicKey, device.CapabilityHash)
	if err != nil {
		return Device{}, false, err
	}
	return device, true, tx.Commit(ctx)
}
