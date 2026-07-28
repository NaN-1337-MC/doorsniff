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

// IsPersistent reports whether this login mode describes the server's
// authentication model (online/offline/whitelisted) rather than a
// transient result of a single probe (disconnect/unknown/full/kicked/banned).
// Persistent modes are written to login_mode; transient ones to last_login.
func (m LoginMode) IsPersistent() bool {
	switch m {
	case LoginModeOnline, LoginModeOffline, LoginModeWhitelisted:
		return true
	}
	return false
}

type LoginResult struct {
	Mode             LoginMode
	DisconnectReason string
}

var (
	// Whitelist patterns cover the common phrasings servers use:
	// - "You are not whitelisted on this server"
	// - "Not on the whitelist"
	// - "This server is whitelisted"
	// - "Whitelist enabled"
	regexWhitelist = regexp.MustCompile(`(?i)whitelist|white[\s_-]?list`)

	// Ban patterns match common phrases like "You are banned from this
	// server", "You have been banned", "Banned: <reason>".
	regexBan = regexp.MustCompile(`(?i)\b(ban|banned|banning)\b`)

	// Full patterns match "Server is full", "Server full", "The server
	// is currently full".
	regexFull = regexp.MustCompile(`(?i)\bserver\s+is\s+full\b|\bserver\s+full\b|\bserver\s+is\s+currently\s+full\b`)

	// Kicked patterns match generic operator kick messages that don't
	// fit whitelist/ban/full.
	regexKicked = regexp.MustCompile(`(?i)\bkicked\b|\byou\s+have\s+been\s+kicked\b`)

	// Version/protocol mismatch — server tells us our client/protocol
	// is outdated or wrong. Some servers send this as a translated
	// component (e.g. multiplayer.disconnect.outdated_client /
	// .outdated_server), others as plain text in their language.
	regexOutdated = regexp.MustCompile(`(?i)outdated_client|outdated_server|outdated client|outdated server|desactualizado|desactualizada|actualizado!|too old|too new|incompatible`)

	// Modded server requires Forge/FML on the client. Caught from the
	// "This server has mods that require Forge/FML..." disconnect.
	regexForge = regexp.MustCompile(`(?i)\b(forge|fml)\b|require.*forge|require.*fml`)

	// Version hint extracted from disconnect messages. Catches:
	//   "Please use version 1.21.11"
	//   "Outdated client! Please use 1.21.11"
	//   "Required version: 1.20.1"
	//   "Use 1.21.11 to connect"
	//   "multiplayer.disconnect.incompatible 1.21.11"
	//   "Your client is outdated, please use 1.20.5"
	// Captures vanilla (1.X.Y) and release-style (26.1.2) versions.
	//
	// The regex is intentionally permissive: any nearby "X.Y.Z" pattern
	// that follows a version-use keyword is assumed to be the version
	// hint. False positives (e.g. a banned message that happens to
	// mention a version number) just trigger an extra retry that fails
	// fast.
	regexVersionHint = regexp.MustCompile(`(?i)(?:use|requires?|need|version|outdated|incompatible|too old|too new|to connect)[\s\w:,]*?(\d+\.\d+(?:\.\d+)?)`)
)

// ExtractVersionHint pulls a version string out of a disconnect reason
// if the server reveals the version it wants. Returns "" when no hint
// is present. The hint is informational only — callers must map the
// version to a protocol number themselves.
func ExtractVersionHint(reason string) string {
	m := regexVersionHint.FindStringSubmatch(reason)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

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
		// Some servers send {"translate":"<key>","with":[...]} without a
		// "text" field. Use the translate key itself plus the "with"
		// arguments joined, so pattern-based classifiers can still match.
		if t, ok := val["translate"].(string); ok && t != "" {
			var b strings.Builder
			b.WriteString(t)
			if with, ok := val["with"].([]interface{}); ok {
				b.WriteByte(' ')
				for _, w := range with {
					b.WriteString(extractTextComponent(w))
				}
			}
			return b.String()
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
	if regexOutdated.MatchString(text) {
		return LoginModeDisconnect
	}
	if regexForge.MatchString(text) {
		return LoginModeDisconnect
	}
	return LoginModeDisconnect
}
