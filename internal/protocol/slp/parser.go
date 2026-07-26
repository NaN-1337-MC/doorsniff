package slp

import (
	"encoding/json"
	"fmt"
)

// Response is the parsed Server List Ping response. Description is
// normalised to a string: if the server sent a chat object (1.20.3+),
// the JSON is preserved verbatim; if it sent a plain string, the
// string is stored as-is.
type Response struct {
	Version     Version       `json:"version"`
	Players     Players       `json:"players"`
	Description string        `json:"description"`
	Favicon     string        `json:"favicon,omitempty"`
	EnforcesSecureChat bool  `json:"enforcesSecureChat,omitempty"`
	PreviewsChat bool         `json:"previewsChat,omitempty"`
}

// Version is the protocol and human-readable version of the server.
type Version struct {
	Name     string `json:"name"`
	Protocol int    `json:"protocol"`
}

// Players is the player count and optional sample list.
type Players struct {
	Max    int            `json:"max"`
	Online int            `json:"online"`
	Sample []SamplePlayer `json:"sample,omitempty"`
}

// SamplePlayer is one entry from players.sample.
type SamplePlayer struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// Parse decodes a Server List Ping JSON response into a Response.
// Description may be a string or a chat object; both forms are
// accepted and stored in Description as a string.
func Parse(data []byte) (*Response, error) {
	var raw struct {
		Version            Version        `json:"version"`
		Players            Players        `json:"players"`
		Description        json.RawMessage `json:"description"`
		Favicon            string         `json:"favicon,omitempty"`
		EnforcesSecureChat bool           `json:"enforcesSecureChat,omitempty"`
		PreviewsChat       bool           `json:"previewsChat,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing SLP response: %w", err)
	}
	desc, err := flattenDescription(raw.Description)
	if err != nil {
		return nil, fmt.Errorf("flattening description: %w", err)
	}
	return &Response{
		Version:            raw.Version,
		Players:            raw.Players,
		Description:        desc,
		Favicon:            raw.Favicon,
		EnforcesSecureChat: raw.EnforcesSecureChat,
		PreviewsChat:       raw.PreviewsChat,
	}, nil
}

// flattenDescription accepts either a JSON string or a JSON chat
// object and returns a string. Strings round-trip as-is; chat objects
// are re-encoded as compact JSON.
func flattenDescription(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		// Try to extract a top-level "text" field for readability.
		if t, ok := obj["text"].(string); ok && t != "" {
			return t, nil
		}
		// Fall back to the full JSON form.
		b, err := json.Marshal(obj)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return string(raw), nil
}
