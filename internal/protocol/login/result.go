package login

import (
	"encoding/json"
	"regexp"
	"strings"
)

type LoginMode string

const (
	LoginModeOnline     LoginMode = "online"
	LoginModeOffline    LoginMode = "offline"
	LoginModeWhitelisted LoginMode = "whitelisted"
	LoginModeBanned     LoginMode = "banned"
	LoginModeKicked     LoginMode = "kicked"
	LoginModeFull       LoginMode = "full"
	LoginModeDisconnect LoginMode = "disconnect"
	LoginModeUnknown    LoginMode = "unknown"
)

func (m LoginMode) String() string {
	return string(m)
}

type LoginResult struct {
	Mode             LoginMode
	DisconnectReason string
}

var (
	regexWhitelist = regexp.MustCompile(`(?i)whitelist`)
	regexBan       = regexp.MustCompile(`(?i)\b(ban|banned)\b`)
	regexFull      = regexp.MustCompile(`(?i)\bfull\b`)
	regexKicked    = regexp.MustCompile(`(?i)kicked`)
)

func DetectMode(reasonJSON string) LoginMode {
	if reasonJSON == "" {
		return LoginModeUnknown
	}

	var raw interface{}
	if err := json.Unmarshal([]byte(reasonJSON), &raw); err != nil {
		return extractFromPlainText(reasonJSON)
	}

	text := extractTextComponent(raw)
	if text == "" {
		return extractFromPlainText(reasonJSON)
	}

	return classifyReason(text)
}

func extractTextComponent(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]interface{}:
		if s, ok := val["text"].(string); ok && s != "" {
			return s
		}
		if extra, ok := val["extra"].([]interface{}); ok {
			var b strings.Builder
			for _, e := range extra {
				b.WriteString(extractTextComponent(e))
			}
			return b.String()
		}
	case []interface{}:
		var b strings.Builder
		for _, e := range val {
			b.WriteString(extractTextComponent(e))
		}
		return b.String()
	}
	return ""
}

func extractFromPlainText(text string) LoginMode {
	text = strings.TrimSpace(text)
	if text == "" {
		return LoginModeUnknown
	}
	return classifyReason(text)
}

func classifyReason(text string) LoginMode {
	if regexWhitelist.MatchString(text) {
		return LoginModeWhitelisted
	}
	if regexBan.MatchString(text) {
		return LoginModeBanned
	}
	if regexFull.MatchString(text) {
		return LoginModeFull
	}
	if regexKicked.MatchString(text) {
		return LoginModeKicked
	}
	return LoginModeDisconnect
}
