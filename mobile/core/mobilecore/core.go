// Package mobilecore is the gomobile-bindable multi-pairing facade used by
// Flutter. Credentials remain inside this package/native storage; Flutter
// receives only metadata and normalized connection state.
package mobilecore

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	endpoint "github.com/vual/work-anywhere/relay/pkg/endpoint/client"
)

const Contract = "mobile-control-v1"

const probeTimeout = 15 * time.Second

type EventSink interface{ OnEvent(eventJSON string) }

type Core struct {
	mu         sync.Mutex
	sink       EventSink
	records    map[string]*pairingRuntime
	activeID   string
	generation uint64
	closed     bool
}

type pairingRuntime struct {
	client    *endpoint.Client
	display   string
	enabled   bool
	order     int
	phase     string
	checkedAt int64
	lastError string
}

type catalog struct {
	Version        int              `json:"version"`
	ActiveDaemonID string           `json:"activeDaemonId,omitempty"`
	Pairings       []catalogPairing `json:"pairings"`
}

type catalogPairing struct {
	DaemonID    string          `json:"daemonId"`
	PairingID   string          `json:"pairingId,omitempty"`
	DisplayName string          `json:"displayName,omitempty"`
	RelayURL    string          `json:"relayUrl"`
	Enabled     bool            `json:"enabled"`
	Order       int             `json:"order"`
	CheckedAt   int64           `json:"checkedAt,omitempty"`
	Credential  json.RawMessage `json:"credential"`
}

type pairingSummary struct {
	DaemonID    string `json:"daemonId"`
	PairingID   string `json:"pairingId,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	RelayURL    string `json:"relayUrl"`
	Enabled     bool   `json:"enabled"`
	Order       int    `json:"order"`
	Phase       string `json:"phase"`
	CheckedAt   int64  `json:"checkedAt,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}

type statusPayload struct {
	Connected      bool             `json:"connected"`
	ActiveDaemonID string           `json:"activeDaemonId,omitempty"`
	Generation     uint64           `json:"generation"`
	Pairings       []pairingSummary `json:"pairings"`
}

func NewCore() *Core { return &Core{records: make(map[string]*pairingRuntime)} }

func (c *Core) SetEventSink(sink EventSink) {
	c.mu.Lock()
	c.sink = sink
	active := c.activeID
	generation := c.generation
	var client *endpoint.Client
	if runtime := c.records[active]; runtime != nil {
		client = runtime.client
	}
	c.mu.Unlock()
	if client != nil {
		c.bindEventSink(active, client, generation)
	}
}

func (c *Core) bindEventSink(daemonID string, client *endpoint.Client, generation uint64) {
	client.SetEventSink(func(eventJSON string) {
		var envelope map[string]any
		if json.Unmarshal([]byte(eventJSON), &envelope) == nil {
			envelope["daemonId"] = daemonID
			envelope["generation"] = generation
			if raw, err := json.Marshal(envelope); err == nil {
				eventJSON = string(raw)
			}
		}
		c.mu.Lock()
		sink := c.sink
		current := c.activeID == daemonID && c.generation == generation
		c.mu.Unlock()
		if sink != nil && current {
			sink.OnEvent(eventJSON)
		}
	})
}

func (c *Core) Pair(uri string) string {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return failure("closed", "mobile core is closed")
	}
	c.mu.Unlock()

	candidate := endpoint.New()
	err := candidate.Pair(context.Background(), uri)
	raw, exportErr := candidate.ExportCredentials()
	if exportErr != nil {
		candidate.Close()
		if err == nil {
			err = exportErr
		}
		return failureFromError(err, "pair_failed")
	}
	var credential endpoint.CredentialSet
	if json.Unmarshal(raw, &credential) != nil || credential.DaemonDeviceID == "" {
		candidate.Close()
		return failure("pair_failed", "pairing credentials are invalid")
	}
	if !validRelayOrigin(credential.RelayURL) {
		candidate.Close()
		return failure("pair_failed", "invalid Relay origin")
	}
	c.promoteCandidate(credential.DaemonDeviceID, candidate, credential.ComputerName)
	if err != nil {
		return failureFromError(err, "pair_failed")
	}
	return success(c.statusValue())
}

// Restore accepts the versioned catalog and the legacy single credential JSON.
func (c *Core) Restore(raw string) string {
	rawBytes := []byte(raw)
	var envelope struct {
		Pairings []json.RawMessage `json:"pairings"`
	}
	if json.Unmarshal(rawBytes, &envelope) != nil {
		return failure("restore_failed", "invalid stored pairing catalog")
	}
	if envelope.Pairings == nil {
		client := endpoint.New()
		if err := client.LoadCredentials(rawBytes); err != nil {
			client.Close()
			return failure("restore_failed", err.Error())
		}
		credential, err := client.ExportCredentials()
		if err != nil {
			client.Close()
			return failure("restore_failed", err.Error())
		}
		client.Close()
		var value endpoint.CredentialSet
		if err := json.Unmarshal(credential, &value); err != nil {
			client.Close()
			return failure("restore_failed", err.Error())
		}
		if !validRelayOrigin(value.RelayURL) {
			return failure("restore_failed", "invalid Relay origin")
		}
		c.replaceCatalog(catalog{Version: 2, ActiveDaemonID: value.DaemonDeviceID, Pairings: []catalogPairing{{DaemonID: value.DaemonDeviceID, PairingID: value.PairingID, DisplayName: value.ComputerName, RelayURL: value.RelayURL, Enabled: true, Order: 0, Credential: credential}}})
		if err := c.connectActive(context.Background()); err != nil {
			return success(c.statusValue())
		}
		return success(c.statusValue())
	}
	var stored catalog
	if err := json.Unmarshal(rawBytes, &stored); err != nil {
		return failure("restore_failed", "invalid stored pairing catalog")
	}
	if stored.Version == 0 {
		stored.Version = 2
	}
	if err := c.loadCatalog(stored); err != nil {
		return failure("restore_failed", err.Error())
	}
	if err := c.connectActive(context.Background()); err != nil {
		return success(c.statusValue())
	}
	return success(c.statusValue())
}

func (c *Core) ExportCredentials() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if runtime := c.records[c.activeID]; runtime != nil {
		return runtime.client.ExportCredentials()
	}
	return nil, errors.New("client has no pairing credentials")
}

// ExportCatalog returns the encrypted-storage payload as JSON text. Keep this
// as a string-returning gomobile method so Android reflection does not expose
// raw byte arrays to the host.
func (c *Core) ExportCatalog() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, err := c.exportCatalogLocked()
	if err != nil {
		return ""
	}
	return string(raw)
}

func (c *Core) SelectPairing(daemonID string) string {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return failure("closed", "mobile core is closed")
	}
	runtime := c.records[daemonID]
	if runtime == nil {
		c.mu.Unlock()
		return failure("pairing_not_found", "pairing was not found")
	}
	if !runtime.enabled {
		c.mu.Unlock()
		return failure("pairing_disabled", "pairing is disabled")
	}
	oldID := c.activeID
	old := c.records[oldID]
	c.activeID = daemonID
	c.generation++
	c.mu.Unlock()
	if old != nil && oldID != daemonID {
		old.client.Disconnect()
	}
	_ = c.connectActive(context.Background())
	return success(c.statusValue())
}

func (c *Core) SetPairingEnabled(daemonID string, enabled bool) string {
	c.mu.Lock()
	runtime := c.records[daemonID]
	if runtime == nil {
		c.mu.Unlock()
		return failure("pairing_not_found", "pairing was not found")
	}
	runtime.enabled = enabled
	runtime.phase = "offline"
	if !enabled {
		runtime.phase = "disabled"
	}
	wasActive := c.activeID == daemonID
	if wasActive && !enabled {
		c.activeID = c.firstEnabledLocked()
		c.generation++
	}
	nextID := c.activeID
	var oldClient *endpoint.Client
	if !enabled || (wasActive && nextID != daemonID) {
		oldClient = runtime.client
	}
	c.mu.Unlock()
	if oldClient != nil {
		oldClient.Disconnect()
	}
	if nextID != "" && (wasActive || (!enabled && nextID != daemonID)) {
		_ = c.connectActive(context.Background())
	}
	return success(c.statusValue())
}

func (c *Core) UpdatePairing(daemonID, displayName, relayURL string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len([]rune(displayName)) > 80 {
		return failure("invalid_display_name", "computer name must contain 1 to 80 characters")
	}
	if !validRelayOrigin(relayURL) {
		return failure("invalid_relay_origin", "Relay address must be an HTTP(S) origin")
	}
	c.mu.Lock()
	runtime := c.records[daemonID]
	if runtime == nil {
		c.mu.Unlock()
		return failure("pairing_not_found", "pairing was not found")
	}
	runtime.display, runtime.phase, runtime.lastError = displayName, "offline", ""
	isActive := c.activeID == daemonID && runtime.enabled
	isEnabled := runtime.enabled
	client := runtime.client
	if isActive {
		c.generation++
	}
	c.mu.Unlock()
	// UpdateRelayURL may synchronously emit a transport event when it closes
	// the old connection. Never hold the registry lock across that callback.
	client.UpdateRelayURL(relayURL)
	if isActive {
		_ = c.connectActive(context.Background())
	} else if isEnabled {
		_ = c.probe(daemonID)
	}
	return success(c.statusValue())
}

func (c *Core) ReorderPairings(ids []string) string {
	c.mu.Lock()
	seen := make(map[string]bool, len(ids))
	order := 0
	for _, id := range ids {
		if runtime := c.records[id]; runtime != nil && !seen[id] {
			runtime.order = order
			seen[id] = true
			order++
		}
	}
	for id, runtime := range c.records {
		if !seen[id] {
			runtime.order = order
			order++
		}
	}
	c.mu.Unlock()
	return success(c.statusValue())
}

func (c *Core) ProbePairing(daemonID string) string {
	_ = c.probe(daemonID)
	return success(c.statusValue())
}

func (c *Core) Call(method, paramsJSON string, timeoutMilliseconds int64) string {
	params := json.RawMessage(paramsJSON)
	if !json.Valid(params) {
		return failure("invalid_params", "paramsJSON must be valid JSON")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return failure("closed", "mobile core is closed")
	}
	runtime := c.records[c.activeID]
	if runtime == nil || !runtime.enabled {
		c.mu.Unlock()
		return failure("no_active_pairing", "no enabled computer is selected")
	}
	activeID, client := c.activeID, runtime.client
	c.mu.Unlock()
	if timeoutMilliseconds <= 0 {
		timeoutMilliseconds = 30000
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMilliseconds)*time.Millisecond)
	defer cancel()
	response, err := client.Call(ctx, method, params)
	if err != nil {
		c.markOffline(activeID, err.Error())
		return failureFromError(err, "call_failed")
	}
	return string(response)
}

func (c *Core) Status() string {
	return success(c.statusValue())
}

func (c *Core) statusValue() statusPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Core) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	clients := make([]*endpoint.Client, 0, len(c.records))
	for _, runtime := range c.records {
		clients = append(clients, runtime.client)
	}
	c.mu.Unlock()
	for _, client := range clients {
		client.Close()
	}
}

func (c *Core) promoteCandidate(daemonID string, client *endpoint.Client, defaultName string) {
	var replaced, previous *endpoint.Client
	c.mu.Lock()
	if old := c.records[daemonID]; old != nil {
		replaced = old.client
		name := old.display
		if name == "" {
			name = defaultName
		}
		c.records[daemonID] = &pairingRuntime{client: client, display: name, enabled: old.enabled, order: old.order, phase: "offline"}
	} else {
		c.records[daemonID] = &pairingRuntime{client: client, display: defaultName, enabled: true, order: c.nextOrderLocked(), phase: "offline"}
	}
	if c.records[daemonID].enabled {
		oldID := c.activeID
		old := c.records[oldID]
		c.activeID = daemonID
		c.generation++
		generation := c.generation
		if old != nil && oldID != daemonID {
			previous = old.client
		}
		c.mu.Unlock()
		if replaced != nil {
			replaced.Close()
		}
		if previous != nil {
			previous.Disconnect()
		}
		c.bindEventSink(daemonID, client, generation)
		return
	}
	c.mu.Unlock()
	if replaced != nil {
		replaced.Close()
	}
	client.Disconnect()
}

func (c *Core) connectActive(ctx context.Context) error {
	c.mu.Lock()
	activeID := c.activeID
	generation := c.generation
	runtime := c.records[activeID]
	if runtime == nil || !runtime.enabled {
		c.mu.Unlock()
		return errors.New("no enabled computer is selected")
	}
	client := runtime.client
	raw, err := client.ExportCredentials()
	c.mu.Unlock()
	if err != nil {
		c.markOffline(activeID, err.Error())
		return err
	}
	err = client.Restore(ctx, raw)
	if err != nil {
		c.markOffline(activeID, err.Error())
		return err
	}
	c.mu.Lock()
	if c.activeID != activeID || c.generation != generation {
		c.mu.Unlock()
		return context.Canceled
	}
	if current := c.records[activeID]; current != nil {
		current.phase, current.lastError, current.checkedAt = "online", "", time.Now().UnixMilli()
	}
	c.mu.Unlock()
	c.bindEventSink(activeID, client, generation)
	return nil
}

func (c *Core) probe(daemonID string) error {
	c.mu.Lock()
	runtime := c.records[daemonID]
	if runtime == nil {
		c.mu.Unlock()
		return errors.New("pairing was not found")
	}
	if !runtime.enabled {
		runtime.phase = "disabled"
		c.mu.Unlock()
		return nil
	}
	raw, err := runtime.client.ExportCredentials()
	c.mu.Unlock()
	if err != nil {
		c.markOffline(daemonID, err.Error())
		return err
	}
	probeClient := endpoint.New()
	defer probeClient.Close()
	probeContext, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	if err = probeClient.Restore(probeContext, raw); err != nil {
		c.markOffline(daemonID, err.Error())
		return err
	}
	if _, err = probeClient.Call(probeContext, "system.status", json.RawMessage(`{}`)); err != nil {
		c.markOffline(daemonID, err.Error())
		return err
	}
	c.mu.Lock()
	if current := c.records[daemonID]; current != nil {
		current.phase, current.lastError, current.checkedAt = "online", "", time.Now().UnixMilli()
	}
	c.mu.Unlock()
	return nil
}

func (c *Core) loadCatalog(stored catalog) error {
	newRecords := make(map[string]*pairingRuntime, len(stored.Pairings))
	seenIDs := make(map[string]struct{}, len(stored.Pairings))
	for _, item := range stored.Pairings {
		if item.DaemonID == "" || len(item.Credential) == 0 {
			return errors.New("stored pairing catalog contains an incomplete record")
		}
		if _, exists := seenIDs[item.DaemonID]; exists {
			return errors.New("stored pairing catalog contains duplicate daemon IDs")
		}
		seenIDs[item.DaemonID] = struct{}{}
		if !validRelayOrigin(item.RelayURL) {
			return errors.New("stored pairing catalog contains an invalid Relay origin")
		}
		var credential endpoint.CredentialSet
		if err := json.Unmarshal(item.Credential, &credential); err != nil || credential.DaemonDeviceID != item.DaemonID {
			return errors.New("stored pairing catalog daemon identity does not match its credential")
		}
		client := endpoint.New()
		if err := client.LoadCredentials(item.Credential); err != nil {
			client.Close()
			return err
		}
		if item.RelayURL != "" {
			client.UpdateRelayURL(item.RelayURL)
		}
		name := strings.TrimSpace(item.DisplayName)
		if name == "" {
			name = item.DaemonID
		}
		phase := "offline"
		if !item.Enabled {
			phase = "disabled"
		}
		newRecords[item.DaemonID] = &pairingRuntime{client: client, display: name, enabled: item.Enabled, order: item.Order, phase: phase, checkedAt: item.CheckedAt}
	}
	c.mu.Lock()
	oldClients := make([]*endpoint.Client, 0, len(c.records))
	for _, runtime := range c.records {
		oldClients = append(oldClients, runtime.client)
	}
	c.records = newRecords
	c.activeID = stored.ActiveDaemonID
	c.generation++
	if current := c.records[c.activeID]; current == nil || !current.enabled {
		c.activeID = c.firstEnabledLocked()
	}
	c.mu.Unlock()
	for _, client := range oldClients {
		client.Close()
	}
	return nil
}

func (c *Core) replaceCatalog(stored catalog) {
	_ = c.loadCatalog(stored)
}

func (c *Core) exportCatalogLocked() ([]byte, error) {
	result := catalog{Version: 2, ActiveDaemonID: c.activeID}
	for daemonID, runtime := range c.records {
		credential, err := runtime.client.ExportCredentials()
		if err != nil {
			return nil, err
		}
		status := runtime.client.Status()
		result.Pairings = append(result.Pairings, catalogPairing{DaemonID: daemonID, PairingID: status.PairingID, DisplayName: runtime.display, RelayURL: statusRelay(credential), Enabled: runtime.enabled, Order: runtime.order, CheckedAt: runtime.checkedAt, Credential: credential})
	}
	sort.Slice(result.Pairings, func(i, j int) bool { return result.Pairings[i].Order < result.Pairings[j].Order })
	return json.Marshal(result)
}

func statusRelay(raw []byte) string {
	var credential endpoint.CredentialSet
	if json.Unmarshal(raw, &credential) == nil {
		return credential.RelayURL
	}
	return ""
}

func (c *Core) statusLocked() statusPayload {
	result := statusPayload{ActiveDaemonID: c.activeID, Generation: c.generation, Pairings: make([]pairingSummary, 0, len(c.records))}
	for daemonID, runtime := range c.records {
		status := runtime.client.Status()
		phase := runtime.phase
		if !runtime.enabled {
			phase = "disabled"
		} else if daemonID == c.activeID && status.Connected {
			phase = "online"
		}
		result.Pairings = append(result.Pairings, pairingSummary{DaemonID: daemonID, PairingID: status.PairingID, DisplayName: runtime.display, RelayURL: statusRelay(mustExport(runtime.client)), Enabled: runtime.enabled, Order: runtime.order, Phase: phase, CheckedAt: runtime.checkedAt, LastError: runtime.lastError})
		result.Connected = result.Connected || daemonID == c.activeID && status.Connected
	}
	sort.Slice(result.Pairings, func(i, j int) bool { return result.Pairings[i].Order < result.Pairings[j].Order })
	return result
}

func mustExport(client *endpoint.Client) []byte {
	raw, _ := client.ExportCredentials()
	return raw
}

func (c *Core) firstEnabledLocked() string {
	ids := make([]string, 0, len(c.records))
	for id, runtime := range c.records {
		if runtime.enabled {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return c.records[ids[i]].order < c.records[ids[j]].order })
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func (c *Core) nextOrderLocked() int {
	max := -1
	for _, runtime := range c.records {
		if runtime.order > max {
			max = runtime.order
		}
	}
	return max + 1
}

func (c *Core) markOffline(daemonID, message string) {
	c.mu.Lock()
	if runtime := c.records[daemonID]; runtime != nil && runtime.enabled {
		runtime.phase, runtime.lastError, runtime.checkedAt = "offline", message, time.Now().UnixMilli()
	}
	c.mu.Unlock()
}

func validRelayOrigin(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && (parsed.Path == "" || parsed.Path == "/") && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func failureFromError(err error, fallback string) string {
	if err == nil {
		return failure(fallback, fallback)
	}
	var callError *endpoint.CallError
	if errors.As(err, &callError) {
		return failure(callError.Code, callError.Message)
	}
	return failure(fallback, err.Error())
}

type resultEnvelope struct {
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  *fault `json:"error,omitempty"`
}

type fault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func success(value any) string {
	raw, err := json.Marshal(resultEnvelope{OK: true, Result: value})
	if err != nil {
		return failure("encode_failed", err.Error())
	}
	return string(raw)
}

func failure(code, message string) string {
	if message == "" {
		message = errors.New(code).Error()
	}
	raw, _ := json.Marshal(resultEnvelope{OK: false, Error: &fault{Code: code, Message: message}})
	return string(raw)
}
