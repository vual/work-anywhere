package installation

// BridgeStatus describes the ChatGPT Codex Desktop integration of this executable.
type BridgeStatus struct {
	Supported    bool   `json:"supported"`
	Installed    bool   `json:"installed"`
	CodexCLIPath string `json:"codexCliPath,omitempty"`
}
