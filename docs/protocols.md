# Protocols

The scanner speaks three Minecraft protocols:

1. **SLP modern 1.7+** (TCP) — the primary discovery method.
2. **SLP legacy 1.6** (TCP) — fallback for BungeeCord / Velocity
   proxies that RST the modern handshake.
3. **Query full stat** (UDP) — enrichment layer that provides the
   plugin list, map name, game type, and player names that SLP
   does not return.

Both protocols are documented at <https://wiki.vg/Server_List_Ping>
and <https://wiki.vg/Query>.

## Conventions

- **Port.** Default for all protocols is 25565.
- **Timeout.** All probes use a 1-second dial + read deadline by
  default. Configurable via `--timeout` and `SWOOMPS_TIMEOUT`.
- **Varint.** Defined in the Minecraft protocol spec. Each byte uses
  7 bits for data and the high bit as a continuation flag. A 32-bit
  value encodes in 1-5 bytes. We implement `WriteVarInt` /
  `ReadVarInt` directly in `internal/protocol/slp/packet.go` — it is
  ~15 lines and avoids pulling a third-party Minecraft protocol
  library.
- **UTF-8.** Modern strings are length-prefixed (`varint` length,
  then UTF-8 bytes).
- **UTF-16BE.** The legacy 1.6 protocol uses UTF-16BE for the entire
  kick packet body. Decoded with `unicode/utf16.Decode`.
- **UDP.** Query uses fixed-size integer fields (big-endian), not
  varints. No connection state; each packet is independent.

## Server List Ping (SLP) — modern 1.7+

This is the protocol the Minecraft client uses to display a server in
the multiplayer list. It is a TCP conversation, request/response.

### Packet 0x00: handshake (client -> server)

```
varint   : 0x00                         # packet id
varint   : protocol_version (e.g. 765)  # 1.20.4
string   : server_address (the host we dialed)
ushort   : server_port (the port we dialed)
varint   : next_state (1 = status)
```

`next_state = 1` selects the status flow.

### Packet 0x00: status request (client -> server)

```
varint : 0x00
```

Empty payload.

### Packet 0x00: status response (server -> client)

```
varint : 0x00
string : JSON payload
```

The JSON has the following shape (fields may be omitted):

```json
{
  "version": {
    "name": "1.20.4",
    "protocol": 765
  },
  "players": {
    "max": 100,
    "online": 42,
    "sample": [
      { "name": "Notch", "id": "..." }
    ]
  },
  "description": {
    "text": "A Minecraft Server"
  },
  "favicon": "data:image/png;base64,..."
}
```

The `description` may be a string or a chat object (since 1.20.3).
The parser collapses both forms into a single string. `favicon` is
discarded (large base64 blob, not useful for indexing).

### Wire encoding notes

- `string` is `varint length` + raw bytes.
- All multibyte values are big-endian.
- The first packet after connect has a length prefix (`varint` total
  bytes of the packet including its id) when reading from the server,
  but the client does not length-prefix its request — it writes the
  packet id followed by the payload in one shot. (This asymmetry is
  documented in wiki.vg and is the source of half the bugs in this
  protocol.)

## Legacy SLP — 1.6

The pre-1.7 protocol is dead simple and the response is delivered as
a kick packet:

```
Client -> Server: [0xFE] [0x01]                              (2 bytes)
Server -> Client: [0xFF] [short BE length] [UTF-16BE data]   (kick packet)
```

The data is a sequence of sections, each introduced by a marker and
terminated by a null byte:

```
§1\0<protocol>\0§2\0<version>\0§3\0<motd>\0§4\0<num>\0§5\0<max>\0
```

| Marker | Field | Example |
|---|---|---|
| `§1` | Protocol ID (string) | `127` |
| `§2` | Version name | `1.6.4` |
| `§3` | MOTD | `A Minecraft Server` |
| `§4` | Online players (int) | `42` |
| `§5` | Max players (int) | `100` |

Modern (1.7+) servers do **not** respond to this protocol. The
caller must use it only as a fallback.

### When the fallback fires

The scanner's worker pool runs the modern handshake on every open
TCP port. Three outcomes:

1. **Handshake completes, JSON parsed** — the server is a modern
   Minecraft server. Result is recorded with `probe_kind = "slp"`.
2. **Connection RST'd after the handshake** — this is the
   fingerprint of a BungeeCord / Velocity proxy without
   `ping_passthrough` enabled. The worker then runs `PingLegacy`
   against the same port. If that succeeds, the result is recorded
   with `probe_kind = "slp-legacy"`.
3. **No response / timeout / parse error** — the port is not a
   Minecraft server. Dropped.

The two protocol attempts are sequential, not parallel. The legacy
path costs an extra round-trip on the common case where the modern
path works; the modern path is preferred and the legacy path is
never tried for ports where the modern handshake completed.

## Limitations

- The legacy handshake is brittle to non-ASCII characters in the
  MOTD. The parser uses `unicode/utf16.Decode` which handles
  surrogate pairs; a malformed response returns an error and the
  port is dropped.
- Modern servers that proxy (e.g. HAProxy, nginx in `proxy_pass`
  mode) can confuse the SLP parser because the response is from the
  proxy, not the backend. There is no mitigation; the recorded
  data is the proxy's view of the world.
- Query must be explicitly enabled on the server (`enable-query=true`
  in `server.properties`). Many servers do not enable it, so a
  Query failure after SLP success is expected and not an error.

## Query Protocol — UDP

Query is a UDP-based protocol introduced in Beta 1.9 that returns
richer server information than SLP. It must be enabled on the server.
swoomps always uses **full stat** mode because it includes the plugin
list and player names.

### Handshake (Type 0x09)

```
Client -> Server:  [FE FD] [09] [session_id 4 bytes]          (7 bytes)
Server -> Client:  [09]     [session_id 4 bytes] [token\0]    (variable)
```

The challenge token is a null-terminated ASCII decimal string. The
client converts it to an int32 and includes it in the stat request.

The token expires every ~30 seconds. It is bound to the client's
IP and port, not to the session ID.

### Full Stat (Type 0x00)

```
Client -> Server:  [FE FD] [00] [session_id 4B] [challenge 4B] [padding 4B]
Server -> Client:  [00]     [session_id 4B] [payload...]
```

The payload has two sections:

1. **K/V section:** preceded by 11-byte `splitnum` prefix
   (`"splitnum\0\x80\x00"`), then null-terminated key-value pairs
   alternating, terminated by an empty key.

2. **Player section:** preceded by 10-byte separator
   (`\x01player_\0\0`), then null-terminated player names, terminated
   by an empty name.

| Key | Example | Description |
|---|---|---|
| `hostname` | `A Minecraft Server` | MOTD |
| `gametype` | `SMP` | Game type (always SMP) |
| `game_id` | `MINECRAFT` | Always MINECRAFT |
| `version` | `1.21.1` | Server version |
| `plugins` | `Paper 1.21.1: Essentials; Vault` | Bukkit-format plugin list |
| `map` | `world` | Current map name |
| `numplayers` | `5` | Online players (string) |
| `maxplayers` | `100` | Max players (string) |
| `hostport` | `25565` | Server port (little-endian short) |
| `hostip` | `127.0.0.1` | Server IP |

### Plugin format

The `plugins` key uses the Bukkit convention:

```
[SERVER_MOD_NAME[: PLUGIN_NAME(; PLUGIN_NAME...)]]
```

Example: `Paper on 1.21.1-R0.1-SNAPSHOT: Essentials 2.19.0; Vault 1.7.3`

Parsed by `query.ParsePlugins()` into `[]struct{ Name, Version string }`.

### Integration in the scanner

After a successful SLP probe, the worker calls `query.Query()` on
the same host:port. If Query responds:

- `QueryEnabled` is set to `true`
- `Map`, `GameType`, and `Plugins` are enriched from Query data
- `SamplePlayers` is enriched with player names (if SLP had none)
- `ProbeKind` is set to `"slp+query"`

If Query fails (expected when the server hasn't enabled it), the
SLP-only data is kept and `ProbeKind` remains `"slp"`.

### Errors

Query uses sentinel errors:

| Error | Meaning |
|---|---|
| `ErrHandshake` | Handshake packet failed |
| `ErrStat` | Stat request failed |
| `ErrBadMagic` | Response magic mismatch |
| `ErrBadSessionID` | Session ID mismatch |
| `ErrBadChallenge` | Invalid challenge token |
| `ErrResponseShort` | Response too short |

## References

- wiki.vg Server List Ping: <https://wiki.vg/Server_List_Ping>
- wiki.vg Server List Ping#1.6: <https://wiki.vg/Server_List_Ping#1.6>
- wiki.vg Query: <https://wiki.vg/Query>
- Minecraft protocol: <https://wiki.vg/Protocol>
