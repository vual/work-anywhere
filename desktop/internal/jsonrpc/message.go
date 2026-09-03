package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
)

type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

func Parse(data []byte) (Message, error) {
	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		return Message{}, err
	}
	if message.Method == "" && len(message.ID) == 0 {
		return Message{}, errors.New("not a JSON-RPC message")
	}
	return message, nil
}

func (m Message) HasID() bool {
	return len(m.ID) > 0 && !bytes.Equal(bytes.TrimSpace(m.ID), []byte("null"))
}

func (m Message) IsRequest() bool      { return m.Method != "" && m.HasID() }
func (m Message) IsNotification() bool { return m.Method != "" && !m.HasID() }
func (m Message) IsResponse() bool     { return m.Method == "" && m.HasID() }

func RewriteID(data []byte, id any) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	replacement, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	object["id"] = replacement
	return json.Marshal(object)
}

func RewriteRawID(data []byte, id json.RawMessage) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	object["id"] = append(json.RawMessage(nil), id...)
	return json.Marshal(object)
}
