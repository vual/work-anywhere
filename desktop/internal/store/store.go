package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/platform/secrets"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	aead cipher.AEAD
}

func Open(databasePath, keyPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return nil, err
	}
	key, err := loadOrCreateMasterKey(keyPath)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, aead: aead}
	if err = store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	statements := []string{
		`PRAGMA journal_mode=WAL`, `PRAGMA synchronous=NORMAL`, `PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS instances (
			id TEXT PRIMARY KEY, name TEXT, codex_home TEXT NOT NULL, user_data_dir TEXT NOT NULL,
			state TEXT NOT NULL, runtime_path TEXT, runtime_version TEXT, runtime_sha256 TEXT,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS runtime_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT, instance_id TEXT NOT NULL, executable TEXT NOT NULL,
			version TEXT, sha256 TEXT, resources_dir TEXT, discovered_by TEXT, seen_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS conversations (
			instance_id TEXT NOT NULL, conversation_id TEXT NOT NULL, title TEXT, cwd TEXT,
			status TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0, sequence INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(instance_id, conversation_id)
		)`,
		`CREATE TABLE IF NOT EXISTS turns (
			instance_id TEXT NOT NULL, conversation_id TEXT NOT NULL, turn_id TEXT NOT NULL,
			status TEXT NOT NULL, input_encrypted BLOB, output_encrypted BLOB,
			started_at TEXT, completed_at TEXT, PRIMARY KEY(instance_id, conversation_id, turn_id)
		)`,
		`CREATE TABLE IF NOT EXISTS timeline_events (
			instance_id TEXT NOT NULL, conversation_id TEXT NOT NULL, sequence INTEGER NOT NULL,
			event_type TEXT NOT NULL, payload_encrypted BLOB, created_at TEXT NOT NULL,
			PRIMARY KEY(instance_id, conversation_id, sequence)
		)`,
		`CREATE TABLE IF NOT EXISTS pending_actions (
			instance_id TEXT NOT NULL, action_id TEXT NOT NULL, method TEXT NOT NULL, status TEXT NOT NULL,
			content_encrypted BLOB, created_at TEXT NOT NULL, resolved_at TEXT,
			PRIMARY KEY(instance_id, action_id)
		)`,
		`CREATE TABLE IF NOT EXISTS attachments (
			id TEXT PRIMARY KEY, instance_id TEXT NOT NULL, project_id TEXT, conversation_id TEXT,
			message_id TEXT, mime_type TEXT, file_name TEXT, path TEXT,
			size INTEGER NOT NULL DEFAULT 0, content_encrypted BLOB,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value_encrypted BLOB, updated_at TEXT NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("store migration: %w", err)
		}
	}
	return s.ensureAttachmentColumns()
}

func (s *Store) ensureAttachmentColumns() error {
	rows, err := s.db.Query(`PRAGMA table_info(attachments)`)
	if err != nil {
		return fmt.Errorf("inspect attachment schema: %w", err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("read attachment schema: %w", err)
		}
		columns[name] = true
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("read attachment schema rows: %w", err)
	}
	for _, column := range []struct {
		name, definition string
	}{
		{"project_id", "TEXT"},
		{"message_id", "TEXT"},
		{"path", "TEXT"},
	} {
		if columns[column.name] {
			continue
		}
		if _, err = s.db.Exec(`ALTER TABLE attachments ADD COLUMN ` + column.name + ` ` + column.definition); err != nil {
			return fmt.Errorf("add attachment column %s: %w", column.name, err)
		}
	}
	return nil
}

func (s *Store) Encrypt(value any) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, nil), nil
}

func (s *Store) Decrypt(data []byte, target any) error {
	if len(data) < s.aead.NonceSize() {
		return errors.New("encrypted value is truncated")
	}
	nonce, ciphertext := data[:s.aead.NonceSize()], data[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, target)
}

type InstanceRecord struct {
	ID, Name, CodexHome, UserDataDir, State    string
	RuntimePath, RuntimeVersion, RuntimeSHA256 string
	ResourcesDir, DiscoveredBy                 string
}

type TimelineEvent struct {
	Sequence  int64
	EventType string
	Payload   any
}

func (s *Store) MaxSequence(instanceID, conversationID string) int64 {
	var sequence int64
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM timeline_events WHERE instance_id=? AND conversation_id=?`, instanceID, conversationID).Scan(&sequence)
	return sequence
}

func (s *Store) ReadEvents(instanceID, conversationID string, after int64, limit int) ([]TimelineEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT sequence,event_type,payload_encrypted FROM timeline_events WHERE instance_id=? AND conversation_id=? AND sequence>? ORDER BY sequence LIMIT ?`, instanceID, conversationID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TimelineEvent, 0)
	for rows.Next() {
		var event TimelineEvent
		var encrypted []byte
		if err = rows.Scan(&event.Sequence, &event.EventType, &encrypted); err != nil {
			return nil, err
		}
		if err = s.Decrypt(encrypted, &event.Payload); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) UpsertInstance(value InstanceRecord) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`INSERT INTO instances(id,name,codex_home,user_data_dir,state,runtime_path,runtime_version,runtime_sha256,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,codex_home=excluded.codex_home,
		user_data_dir=excluded.user_data_dir,state=excluded.state,runtime_path=excluded.runtime_path,
		runtime_version=excluded.runtime_version,runtime_sha256=excluded.runtime_sha256,updated_at=excluded.updated_at`,
		value.ID, value.Name, value.CodexHome, value.UserDataDir, value.State, value.RuntimePath, value.RuntimeVersion, value.RuntimeSHA256, now, now)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO runtime_history(instance_id,executable,version,sha256,resources_dir,discovered_by,seen_at)
		SELECT ?,?,?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM runtime_history WHERE instance_id=? AND sha256=?)`, value.ID, value.RuntimePath, value.RuntimeVersion, value.RuntimeSHA256, value.ResourcesDir, value.DiscoveredBy, now, value.ID, value.RuntimeSHA256)
	return err
}

func (s *Store) UpsertConversation(instanceID, conversationID, status string, revision, sequence int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`INSERT INTO conversations(instance_id,conversation_id,status,revision,sequence,created_at,updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(instance_id,conversation_id) DO UPDATE SET status=CASE WHEN excluded.sequence >= conversations.sequence THEN excluded.status ELSE conversations.status END,revision=MAX(conversations.revision,excluded.revision),sequence=MAX(conversations.sequence,excluded.sequence),updated_at=MAX(conversations.updated_at,excluded.updated_at)`, instanceID, conversationID, status, revision, sequence, now, now)
	return err
}

// ConversationVersion restores independently persisted business and event versions.
func (s *Store) ConversationVersion(instanceID, conversationID string) (status string, revision, sequence int64) {
	_ = s.db.QueryRow(`SELECT status,revision,sequence FROM conversations WHERE instance_id=? AND conversation_id=?`, instanceID, conversationID).Scan(&status, &revision, &sequence)
	return
}

func (s *Store) UpsertTurn(instanceID, conversationID, turnID, status string) error {
	if turnID == "" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`INSERT INTO turns(instance_id,conversation_id,turn_id,status,started_at) VALUES(?,?,?,?,?)
		ON CONFLICT(instance_id,conversation_id,turn_id) DO UPDATE SET status=excluded.status,completed_at=CASE WHEN excluded.status IN ('completed','failed','interrupted') THEN excluded.started_at ELSE turns.completed_at END`, instanceID, conversationID, turnID, status, now)
	return err
}

func (s *Store) SavePendingAction(instanceID, actionID, method string, content any) error {
	encrypted, err := s.Encrypt(content)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO pending_actions(instance_id,action_id,method,status,content_encrypted,created_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(instance_id,action_id) DO UPDATE SET method=excluded.method,status='pending',content_encrypted=excluded.content_encrypted,created_at=excluded.created_at,resolved_at=NULL`, instanceID, actionID, method, "pending", encrypted, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) ResolvePendingAction(instanceID, actionID string) error {
	_, err := s.db.Exec(`UPDATE pending_actions SET status='resolved',resolved_at=? WHERE instance_id=? AND action_id=?`, time.Now().UTC().Format(time.RFC3339Nano), instanceID, actionID)
	return err
}

func (s *Store) AppendEvent(instanceID, conversationID string, sequence int64, eventType string, payload any) error {
	encrypted, err := s.Encrypt(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO timeline_events(instance_id,conversation_id,sequence,event_type,payload_encrypted,created_at) VALUES(?,?,?,?,?,?)`,
		instanceID, conversationID, sequence, eventType, encrypted, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

type Attachment struct {
	ID             string
	InstanceID     string
	ProjectID      string
	ConversationID string
	MessageID      string
	MimeType       string
	FileName       string
	Path           string
	Size           int64
}

func (s *Store) SaveAttachmentMetadata(value Attachment) error {
	if value.ID == "" || value.InstanceID == "" || value.Path == "" {
		return errors.New("attachment metadata is incomplete")
	}
	_, err := s.db.Exec(`INSERT INTO attachments(id,instance_id,project_id,conversation_id,message_id,mime_type,file_name,path,size,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.InstanceID, value.ProjectID, value.ConversationID, value.MessageID, value.MimeType, value.FileName, value.Path, value.Size, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) ReadAttachment(id string) (Attachment, error) {
	var value Attachment
	var projectID, messageID, path sql.NullString
	err := s.db.QueryRow(`SELECT id,instance_id,project_id,conversation_id,message_id,mime_type,file_name,path,size FROM attachments WHERE id=?`, id).Scan(
		&value.ID, &value.InstanceID, &projectID, &value.ConversationID, &messageID, &value.MimeType, &value.FileName, &path, &value.Size,
	)
	if err != nil {
		return Attachment{}, err
	}
	value.ProjectID, value.MessageID, value.Path = projectID.String, messageID.String, path.String
	return value, nil
}

// MaterializeLegacyAttachment migrates an attachment written by the old
// database-backed implementation. New uploads never use content_encrypted.
func (s *Store) MaterializeLegacyAttachment(id, path, projectID, messageID string) (Attachment, error) {
	value, err := s.ReadAttachment(id)
	if err != nil {
		return Attachment{}, err
	}
	if value.Path != "" {
		return value, nil
	}
	var encrypted []byte
	if err = s.db.QueryRow(`SELECT content_encrypted FROM attachments WHERE id=?`, id).Scan(&encrypted); err != nil {
		return Attachment{}, err
	}
	if len(encrypted) == 0 {
		return Attachment{}, errors.New("legacy attachment has no content")
	}
	var content []byte
	if err = s.Decrypt(encrypted, &content); err != nil {
		return Attachment{}, fmt.Errorf("decrypt legacy attachment: %w", err)
	}
	if err = writeAttachmentFile(path, content); err != nil {
		return Attachment{}, err
	}
	_, err = s.db.Exec(`UPDATE attachments SET project_id=?,message_id=?,path=?,size=?,content_encrypted=NULL WHERE id=?`,
		projectID, messageID, path, len(content), id)
	if err != nil {
		_ = os.Remove(path)
		return Attachment{}, err
	}
	value.ProjectID, value.MessageID, value.Path, value.Size = projectID, messageID, path, int64(len(content))
	return value, nil
}

func writeAttachmentFile(path string, content []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".legacy-attachment-*.part")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(content)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func (s *Store) PutSetting(key string, value any) error {
	if key == "" {
		return errors.New("setting key is required")
	}
	encrypted, err := s.Encrypt(value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings(key,value_encrypted,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value_encrypted=excluded.value_encrypted,updated_at=excluded.updated_at`, key, encrypted, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetSetting(key string, target any) (bool, error) {
	var encrypted []byte
	err := s.db.QueryRow(`SELECT value_encrypted FROM settings WHERE key=?`, key).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, s.Decrypt(encrypted, target)
}

func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key=?`, key)
	return err
}

func loadOrCreateMasterKey(path string) ([]byte, error) {
	if protected, err := os.ReadFile(path); err == nil {
		key, unprotectErr := secrets.UnprotectForCurrentUser(protected)
		if unprotectErr != nil {
			return nil, unprotectErr
		}
		if len(key) != 32 {
			return nil, errors.New("invalid master key size")
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	protected, err := secrets.ProtectForCurrentUser(key)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err = os.WriteFile(path, protected, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}
