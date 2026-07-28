package login

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	nextStateLogin int32 = 2

	// protocolVersionLegacy (-1) tells the server to ping without a
	// specific protocol version. Servers respond with a status response
	// regardless of client version. Useful for probing servers that
	// don't speak modern SLP.
	protocolVersionLegacy int32 = -1

	// detectReadDeadline is the per-attempt read deadline used by
	// StealthDetect. It is shorter than the caller's timeout to bias
	// towards giving up early: once we have something to classify we
	// do not want to wait for the server to finish broadcasting
	// "X joined the game".
	detectReadDeadline = 300 * time.Millisecond

	// intrusiveReadDeadline is the per-attempt read deadline used by
	// StealthIntrusive. Long enough for the server to process the
	// login attempt and respond (and possibly broadcast) before we
	// close the connection.
	intrusiveReadDeadline = 5 * time.Second
)

// StealthMode controls how aggressive the login probe is. Ordered
// from most stealthy (off) to least (intrusive).
type StealthMode string

const (
	// StealthOff skips the login handshake entirely. SLP still runs,
	// so the server only sees a harmless status request from our IP.
	StealthOff StealthMode = "off"

	// StealthDetect sends the Login Start packet and tears the
	// connection down as soon as we read the first reply (or the
	// short read deadline expires). Goal: classify the server
	// (offline / online / whitelisted / banned / version mismatch)
	// without giving the server enough wall-clock time to fully
	// process us as a player. Note: this is best-effort — the server
	// may still broadcast "X joined the game" if it processes the
	// Login Start before it observes our FIN.
	StealthDetect StealthMode = "detect"

	// StealthIntrusive closes the connection only after the
	// configured timeout elapses, deliberately letting the server
	// finish processing the Login Start. Use this when you want to
	// stress a server's join pipeline (or trigger the broadcast you
	// do not want in detect).
	StealthIntrusive StealthMode = "intrusive"
)

// DefaultStealthMode is the mode used when --login-mode is not
// supplied. Detect is the closest to a "probe but don't actually
// join" behaviour.
const DefaultStealthMode = StealthDetect

// ParseStealthMode normalises a CLI value to a StealthMode. The
// empty string maps to DefaultStealthMode so that absence-of-flag
// behaves like the legacy --login=true default.
func ParseStealthMode(s string) (StealthMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return DefaultStealthMode, nil
	case "off":
		return StealthOff, nil
	case "detect":
		return StealthDetect, nil
	case "intrusive":
		return StealthIntrusive, nil
	}
	return "", fmt.Errorf("invalid login mode %q (want off|detect|intrusive)", s)
}

func (m StealthMode) Valid() bool {
	switch m {
	case StealthOff, StealthDetect, StealthIntrusive:
		return true
	}
	return false
}

func (m StealthMode) String() string {
	if m == "" {
		return string(DefaultStealthMode)
	}
	return string(m)
}

var (
	ErrConnectionFailed = fmt.Errorf("login: connection failed")
	ErrTimeout          = fmt.Errorf("login: timeout")
	ErrMalformedPacket  = fmt.Errorf("login: malformed packet")
	// ErrLoginSkipped is returned by Handshake when mode == off.
	// Callers should treat it as "no info, not an error".
	ErrLoginSkipped = errors.New("login: skipped (mode=off)")
)

// Handshake performs a Minecraft login handshake against the target
// server and returns the detected mode (offline/online/whitelisted/...).
// The caller may pass a non-nil logger to receive debug-level events
// (raw packet bytes, disconnect JSON, etc.) useful for diagnosing why
// certain servers show up only as "-" or "disconnect".
//
// The protoVersion argument is the protocol version sent in the
// Handshake packet. Pass -1 to use legacy / version-agnostic mode. For
// the common case of probing a server we already have an SLP response
// for, pass the protocol version reported by SLP — this avoids the
// "outdated_client" disconnect that modern servers send when the
// handshake protocol version doesn't match the server's expected range.
//
// If the first handshake fails with an outdated/incompatible message,
// Handshake automatically retries with the version the server hints at
// (extracted from the disconnect reason). The hint-based retry only
// kicks in when the disconnect suggests a different protocol version
// (e.g. "Please use version 1.21.11"); whitelist/ban/empty reasons do
// not trigger a retry.
//
// The mode argument controls how aggressive the probe is:
//
//   - StealthOff: no handshake at all; returns (nil, ErrLoginSkipped).
//   - StealthDetect: send the Login Start packet and tear down the
//     connection as soon as the first reply arrives (or after a short
//     deadline). Best-effort stealth: the server may still broadcast
//     "X joined the game" if it processes Login Start before observing
//     our FIN.
//   - StealthIntrusive: same as Detect but with a long deadline, so
//     the server has time to fully process the Login Start. Use this
//     to stress the server's join pipeline. (The legacy --login=true
//     flag maps to this mode.)
//
// An empty mode is treated as DefaultStealthMode (detect).
func Handshake(ctx context.Context, host string, port uint16, timeout time.Duration, username string, protoVersion int32, mode StealthMode, logger *slog.Logger) (*LoginResult, error) {
	if mode == "" {
		mode = DefaultStealthMode
	}
	if mode == StealthOff {
		return nil, ErrLoginSkipped
	}

	result, err := doHandshakeOnce(ctx, host, port, timeout, username, protoVersion, mode, logger)
	if err != nil {
		return nil, err
	}
	if result.Mode == LoginModeDisconnect || result.Mode == LoginModeUnknown {
		if hint := ExtractVersionHint(result.DisconnectReason); hint != "" {
			hintProto := ProtocolForVersion(hint)
			if hintProto != protoVersion && hintProto != protocolVersionLegacy {
				if logger != nil {
					logger.Debug("login retry with hinted version",
						"addr", net.JoinHostPort(host, strconv.Itoa(int(port))),
						"hint", hint,
						"old_proto", protoVersion,
						"new_proto", hintProto,
					)
				}
				if retried, err := doHandshakeOnce(ctx, host, port, timeout, username, hintProto, mode, logger); err == nil {
					result = retried
				}
			}
		}
	}
	return result, nil
}

// doHandshakeOnce performs a single login handshake attempt without
// retry logic. Used by Handshake to avoid duplicating the dial/send/
// read code. mode controls the read deadline: detect clamps it to
// detectReadDeadline (biased towards tearing down before the server
// finishes processing); intrusive uses the full timeout.
func doHandshakeOnce(ctx context.Context, host string, port uint16, timeout time.Duration, username string, protoVersion int32, mode StealthMode, logger *slog.Logger) (*LoginResult, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))

	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		if logger != nil {
			logger.Debug("login dial failed", "addr", addr, "err", err)
		}
		return nil, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}
	defer conn.Close()

	readDeadline := timeout
	switch mode {
	case StealthDetect:
		if readDeadline > detectReadDeadline {
			readDeadline = detectReadDeadline
		}
	case StealthIntrusive:
		// Use the caller-supplied timeout; if they passed a tiny
		// value, fall back to a sane floor so the server has time
		// to actually process.
		if readDeadline < intrusiveReadDeadline {
			readDeadline = intrusiveReadDeadline
		}
	}

	if err := conn.SetDeadline(time.Now().Add(readDeadline)); err != nil {
		return nil, fmt.Errorf("login: setting deadline: %w", err)
	}

	reader := bufio.NewReader(conn)

	if err := sendHandshake(conn, host, port, protoVersion); err != nil {
		if logger != nil {
			logger.Debug("login handshake send failed", "addr", addr, "err", err)
		}
		return nil, fmt.Errorf("login: sending handshake: %w", err)
	}

	if err := sendLoginStart(conn, username); err != nil {
		if logger != nil {
			logger.Debug("login start send failed", "addr", addr, "err", err)
		}
		return nil, fmt.Errorf("login: sending login start: %w", err)
	}

	result, rawPacket, err := readFirstPacket(reader, logger, addr)
	if err != nil {
		if logger != nil {
			logger.Debug("login readFirstPacket failed", "addr", addr, "err", err, "raw_hex", hex.EncodeToString(rawPacket))
		}
		return nil, err
	}

	if logger != nil {
		logger.Debug("login result",
			"addr", addr,
			"mode", string(result.Mode),
			"mode_requested", string(mode),
			"disconnect_reason", result.DisconnectReason,
		)
	}

	return result, nil
}

// ProtocolVersionLegacy returns the protocol version value that means
// "don't specify a protocol version" in the Handshake packet (-1).
// Useful as the default protoVersion when probing a server whose SLP
// response didn't include a protocol number.
func ProtocolVersionLegacy() int32 {
	return protocolVersionLegacy
}

func sendHandshake(w io.Writer, host string, port uint16, protoVersion int32) error {
	var payload bytes.Buffer
	if err := WriteVarInt(&payload, 0x00); err != nil {
		return err
	}
	if err := WriteVarInt(&payload, protoVersion); err != nil {
		return err
	}
	if err := WriteString(&payload, host); err != nil {
		return err
	}
	if err := WriteUInt16(&payload, port); err != nil {
		return err
	}
	if err := WriteVarInt(&payload, nextStateLogin); err != nil {
		return err
	}

	var pkt bytes.Buffer
	if err := WriteVarInt(&pkt, int32(payload.Len())); err != nil {
		return err
	}
	if _, err := pkt.Write(payload.Bytes()); err != nil {
		return err
	}

	_, err := w.Write(pkt.Bytes())
	return err
}

func sendLoginStart(w io.Writer, username string) error {
	var payload bytes.Buffer
	if err := WriteVarInt(&payload, packetIDLoginStart); err != nil {
		return err
	}
	if err := WriteString(&payload, username); err != nil {
		return err
	}

	uuid := offlinePlayerUUID(username)
	if err := WriteUUID(&payload, uuid); err != nil {
		return err
	}

	var pkt bytes.Buffer
	if err := WriteVarInt(&pkt, int32(payload.Len())); err != nil {
		return err
	}
	if _, err := pkt.Write(payload.Bytes()); err != nil {
		return err
	}

	_, err := w.Write(pkt.Bytes())
	return err
}

func offlinePlayerUUID(name string) [16]byte {
	sum := md5.Sum([]byte("OfflinePlayer:" + name))
	// Minecraft sets the version (4 high bits of byte 6) to 3 and the
	// variant (2 high bits of byte 8) to 10b so the UUID is recognised
	// as a v3 (MD5 namespace) UUID.
	sum[6] = (sum[6] & 0x0f) | 0x30
	sum[8] = (sum[8] & 0x3f) | 0x80
	return sum
}

func readFirstPacket(r *bufio.Reader, logger *slog.Logger, addr string) (*LoginResult, []byte, error) {
	packetLen, err := ReadVarInt(r)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: reading packet length: %v", ErrMalformedPacket, err)
	}
	if packetLen <= 0 || packetLen > 2<<20 {
		return nil, nil, fmt.Errorf("%w: invalid length %d", ErrMalformedPacket, packetLen)
	}

	packetBuf := make([]byte, packetLen)
	if _, err := io.ReadFull(r, packetBuf); err != nil {
		return nil, packetBuf, fmt.Errorf("%w: reading packet body: %v", ErrMalformedPacket, err)
	}

	br := bytes.NewReader(packetBuf)
	packetID, n, err := readVarIntFromBytes(br)
	if err != nil {
		return nil, packetBuf, fmt.Errorf("%w: reading packet id: %v", ErrMalformedPacket, err)
	}

	remaining := packetBuf[n:]

	if logger != nil {
		logger.Debug("login packet received",
			"addr", addr,
			"packet_len", packetLen,
			"packet_id", packetID,
			"body_len", len(remaining),
			"raw_hex", hex.EncodeToString(packetBuf),
		)
	}

	switch packetID {
	case packetIDLoginSuccess:
		return &LoginResult{Mode: LoginModeOffline}, packetBuf, nil

	case packetIDEncryptionReq:
		return &LoginResult{Mode: LoginModeOnline}, packetBuf, nil

	case packetIDSetCompression:
		// SetCompression threshold packet means the server wants to
		// enable zlib compression on subsequent packets. We don't
		// support compressed follow-up packets here, so report it
		// as disconnect (server responded with something that wasn't
		// success or encryption — usually a version mismatch).
		return &LoginResult{Mode: LoginModeDisconnect, DisconnectReason: "server sent SetCompression (likely version mismatch)"}, packetBuf, nil

	case packetIDDisconnect:
		reason := parseDisconnectReason(remaining)
		return &LoginResult{
			Mode:             DetectMode(reason),
			DisconnectReason: reason,
		}, packetBuf, nil

	default:
		if logger != nil {
			logger.Debug("login unknown packet id", "addr", addr, "packet_id", packetID)
		}
		return &LoginResult{Mode: LoginModeUnknown}, packetBuf, nil
	}
}

// readVarIntFromBytes reads a VarInt directly from a *bytes.Reader and
// returns the value plus the number of bytes consumed. Using a
// *bytes.Reader (no bufio wrapper) lets us track exact position via
// br.Seek(0, io.SeekCurrent), which is reliable regardless of any
// internal buffering layer.
func readVarIntFromBytes(br *bytes.Reader) (int32, int, error) {
	start, _ := br.Seek(0, io.SeekCurrent)
	var result int32
	var shift uint
	for i := 0; i < 5; i++ {
		b, err := br.ReadByte()
		if err != nil {
			return 0, 0, err
		}
		result |= int32(b&0x7F) << shift
		if b&0x80 == 0 {
			pos, _ := br.Seek(0, io.SeekCurrent)
			return result, int(pos - start), nil
		}
		shift += 7
	}
	return 0, 0, ErrVarIntTooLong
}

func parseDisconnectReason(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	br := bufio.NewReader(bytes.NewReader(data))

	text, err := readTextComponent(br)
	if err != nil {
		return string(data)
	}
	return text
}

func readTextComponent(r *bufio.Reader) (string, error) {
	length, err := ReadVarInt(r)
	if err != nil {
		return "", err
	}
	if length < 0 || length > 32767 {
		return "", nil
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}

	var raw interface{}
	if err := json.Unmarshal(buf, &raw); err != nil {
		return string(buf), nil
	}

	return extractJSONText(raw), nil
}

func extractJSONText(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]interface{}:
		if s, ok := val["text"].(string); ok {
			return s
		}
		if t, ok := val["translate"].(string); ok {
			result := t
			if with, ok := val["with"].([]interface{}); ok {
				for _, w := range with {
					result += " " + extractJSONText(w)
				}
			}
			return result
		}
		if extra, ok := val["extra"].([]interface{}); ok {
			result := ""
			for _, e := range extra {
				result += extractJSONText(e)
			}
			return result
		}
	case []interface{}:
		result := ""
		for _, e := range val {
			result += extractJSONText(e)
		}
		return result
	}
	return ""
}
