# Ethics

A network scanner is a sensitive tool. This document records the
boundaries the project observes, the user responsibilities, and the
legal background.

## What swoomps does

`swoomps` reads a user-supplied list of seed IPs and scans a
configurable port range against each one. For every port that
responds with a TCP handshake, it attempts the Minecraft Server List
Ping (modern 1.7+ handshake, with a 1.6 fallback for BungeeCord-style
proxies that RST the modern handshake). The output is a local
database of discovered servers.

The protocol interactions are identical to what the official
Minecraft client performs when displaying the multiplayer server
list. They are not exploitation, fuzzing, or denial-of-service
traffic.

## What swoomps does not do

- It does not send RCON commands.
- It does not authenticate, brute-force, or try credentials.
- It does not exfiltrate data to a remote service.
- It does not distribute the collected database.
- It does not scan random IPv4 addresses. The user provides seed
  IPs. CIDR expansion is opt-in (the user puts a CIDR in their
  seed file) and the user controls its size.
- It does not run the Query protocol yet (the driver is in the
  code but is not wired into the scanner).
- It does not implement a blacklist or opt-out mechanism. Server
  operators who want to be excluded must contact the user directly.

## Bounded port scan

The scan is bounded in three ways:

1. **Seeds are user-supplied.** The scanner does not invent
   targets. The `--file PATH` flag or a file in the seeds directory
   is the only source.
2. **CIDR expansion is opt-in and bounded.** When a seed file
   contains a CIDR like `69.9.176.0/20`, the scanner expands it to
   one seed per IP in the range. The user controls the size by
   choosing which CIDRs to include. There is no `0.0.0.0/0` wide
   scan.
3. **Rate limit is global and configurable.** The default is 200
   QPS across all workers. This is high enough to scan a /20 in
   minutes but low enough to stay below the noise floor of any
   reasonable operator. Lower it with `--qps` when in doubt.

## The user's responsibilities

By running `swoomps probe`, the user agrees to:

1. **Respect local law.** Computer-misuse statutes vary by
   jurisdiction. Some countries treat unsolicited network probing
   as a civil or criminal offence even when the protocol is
   harmless. The user is responsible for knowing the law where
   they run.
2. **Respect the target's terms of service.** Many hosting
   providers explicitly prohibit scanning in their ToS or AUP.
   The user must review the ToS of any IP range they scan.
3. **Stay below the noise floor.** The default rate limit is 200
   QPS across all workers. The user can and should lower it where
   they are uncertain.
4. **Not publish the data.** The database is for personal use.
   Public release of harvested lists is a separate ethical
   question and is outside the scope of this project.

## Rate limiting

The default rate limit is 200 QPS, configurable downward via
`--qps` or `SWOOMPS_QPS`. The minimum is 1 QPS. The scanner will
not exceed the configured rate even when the worker pool is large.

In practice, the throughput bottleneck is TCP dial latency, not
the rate limiter. A scan with 256 workers and 200 QPS will hit
roughly 200 successful probes per second when targets respond
quickly, and many more dial attempts that resolve to "no
response" before the 1-second timeout.

## Data handling

- The database is local. The scanner does not phone home.
- No analytics, no telemetry, no debug trace is sent externally.
- `log/slog` writes to stderr by default. The user can redirect.

## Legal background (informational, not legal advice)

The act of sending a TCP SYN and reading a TCP response is, in
most jurisdictions, a normal interaction on a public network. The
legal exposure comes from:

- Repetition designed to disrupt the target (denial of service).
- Bypassing authentication or access controls.
- Accessing systems the user is not authorised to access.

`swoomps` does none of these. The Minecraft protocols it speaks
are publicly documented and intended to be queried by any client.
The project's posture is that this is closer to `dig` than to a
port scanner, but the user should still satisfy themselves that
their use case is lawful and aligned with the target's policies.

## Reporting

If a server operator wants to flag an issue with the scanner's
behaviour, the contact is the repository's issue tracker.

## Future

- A `blacklist.txt` at `$XDG_CONFIG_HOME/swoomps/blacklist.txt`
  that drops any target whose IP is in the file before probing.
  Not yet implemented.
- A DNS TXT record at `_swoomps.<server-hostname>` for opt-out.
  Not yet implemented.
- A GeoIP enrichment so the user can see which hosting regions
  their scan is touching. Not yet implemented.
