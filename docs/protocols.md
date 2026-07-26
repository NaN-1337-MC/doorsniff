# Protocols

The scanner speaks the Minecraft Server List Ping in two variants:
the modern 1.7+ handshake (TCP) and the legacy 1.6 ping (TCP). The
modern path is always tried first; the legacy path is used as a
fallback when the modern handshake is RST'd by a proxy
(BungeeCord / Velocity without `ping_passthrough`).

A future version will add the Query protocol (UDP). The driver lives
at `internal/protocol/query/query.go` but is not wired into the
scanner yet.

Both protocols are documented at <https://wiki.vg/Server_List_Ping>
and <https://wiki.vg/Query>.

## Conventions

- **Port.** Default for both protocols is 25565.
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
- The Query protocol is not yet implemented in the scanner, even
  though the protocol code lives in `internal/protocol/query/`. It
  will be wired in once the TUI and `--format json` features land.

## References

- wiki.vg Server List Ping: <https://wiki.vg/Server_List_Ping>
- wiki.vg Server List Ping#1.6: <https://wiki.vg/Server_List_Ping#1.6>
- wiki.vg Query: <https://wiki.vg/Query>
- Minecraft protocol: <https://wiki.vg/Protocol>
