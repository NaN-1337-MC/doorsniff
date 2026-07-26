# Protocols

The scanner speaks two Minecraft wire protocols, both first documented at
<https://wiki.vg/Server_List_Ping> and <https://wiki.vg/Query>. We support
only the modern variants (handshake 1.7+).

## Conventions

- **Port.** Default for both protocols is 25565.
- **Timeout.** All probes use a 3-second dial + read deadline. Configurable
  via `config.toml` and `SWOOMPS_TIMEOUT`.
- **Varint.** Defined in the Minecraft protocol spec. Each byte uses 7
  bits for data and the high bit as a continuation flag. A 32-bit value
  encodes in 1-5 bytes. We implement `WriteVarInt` / `ReadVarInt` directly
  in `internal/protocol/slp/packet.go` — it is ~15 lines and avoids
  pulling a third-party Minecraft protocol library.
- **UTF-8.** All strings are length-prefixed (`varint` length, then UTF-8
  bytes).

## Server List Ping (SLP) — 1.7+

This is the protocol the Minecraft client uses to display a server in the
multiplayer list. It is a TCP conversation, request/response.

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

The `description` may be a string or a chat object (since 1.20.3). The
parser collapses both forms into a single string.

### Wire encoding notes

- `string` is `varint length` + raw bytes.
- All multibyte values are big-endian.
- The first packet after connect has a length prefix (`varint` total bytes
  of the packet including its id) when reading from the server, but the
  client does not length-prefix its request — it writes the packet id
  followed by the payload in one shot. (This asymmetry is documented in
  wiki.vg and is the source of half the bugs in this protocol.)

## Query protocol (UDP)

An older protocol that returns more detail than SLP, including a list of
online players and a plugin list. The server must have `enable-query=true`
in `server.properties` for the UDP endpoint to respond.

### Handshake

1. Client sends a "magic" packet to UDP/25565:

   ```
   0xFE 0xFD 0x09 0x00 0x00 0x00 0x01 0x00 0x00 0x00
   ```

   The last four bytes are a placeholder session id that the server is
   expected to overwrite.

2. Server replies with the session id and a challenge token:

   ```
   0x09 0x00 0x00 0x00 0x01 <session_id> 0x00 <challenge_token> 0x00
   ```

3. Client sends a stat request with the session id and challenge token:

   ```
   0xFE 0xFD 0x00 0x00 0x00 0x01 <session_id> <challenge_token> 0x00 0x00 0x00 0x00
   ```

   The trailing zeros declare the request type (0x00 = basic, 0x01 = full).

### Basic stat (request type 0x00)

The response is a `KOTLIN_STRING_SEP`-separated list. Different clients
handle the separator differently; the canonical value is `\x00`. The
relevant fields are:

- `hostname`
- `gametype`
- `game_id`
- `version`
- `plugins`
- `map`
- `numplayers`
- `maxplayers`
- `hostport`
- `hostip`

Plugin list is itself a `;`-separated list of `name version`.

### Full stat (request type 0x01)

Same separators, plus three padding fields, then a `section1` of
`key=val` pairs separated by `\x00`:

```
hostname   = A Minecraft Server
gametype   = SMP
game_id    = MINECRAFT
version    = 1.20.4
plugins    = ...
map        = world
numplayers = 42
maxplayers = 100
hostport   = 25565
hostip     = 0.0.0.0
```

And a `section2` of online players, prefixed with `player_` and
separated by `\x00`:

```
player_¬ notch
player_¬ jeb_
```

The parser collapses both sections into a single struct.

### Limitations

- The handshake is brittle to NATs and load balancers; the session id is
  not source-port bound, so simultaneous probes from the same IP can
  confuse the server.
- Many servers have `enable-query=false` (the default). A missing
  response after the handshake is a normal outcome, not a failure.
- The protocol is undocumented beyond wiki.vg and several
  community-decompiled clients. We treat any unexpected byte sequence as
  "no usable data" and log a warning, not an error.

## References

- wiki.vg Server List Ping: <https://wiki.vg/Server_List_Ping>
- wiki.vg Query: <https://wiki.vg/Query>
- Minecraft protocol: <https://wiki.vg/Protocol>
