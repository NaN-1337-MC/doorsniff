# Ethics

A network scanner is a sensitive tool. This document records the
boundaries the project observes, the user responsibilities, and the
legal background.

## What swoomps does

`swoomps` reads a user-supplied list of seed IPs and scans a
configurable port range against each one. For every port that responds
with a TCP handshake, it attempts the Minecraft Server List Ping
(TCP) and Query (UDP) protocols. The output is a local database of
discovered servers.

The protocol interactions are identical to what the official Minecraft
client performs when displaying the multiplayer server list. They are
not exploitation, fuzzing, or denial-of-service traffic.

## What swoomps does not do

- It does not send RCON commands.
- It does not authenticate, brute-force, or try credentials.
- It does not exfiltrate data to a remote service.
- It does not distribute the collected database.
- It does not scan random IPv4 addresses. The user provides seed IPs.
  CIDR expansion is bounded (see below).

## Bounded port scan

The scan is bounded in three ways:

1. **Seeds are user-supplied.** The scanner does not invent targets.
   The `--seeds` flag or a `seeds.txt` file is the only source.
2. **CIDR expansion is opt-out and bounded.** When enabled, the
   scanner only walks the `/24` neighbours of each seed IP (256
   addresses each). It does not march across arbitrary large ranges.
   Pass `--no-expand` to disable.
3. **Rate limit is global and configurable.** The default is 50 QPS
   across all workers. This is far below the rate at which any sane
   operator would react, but the user can and should lower it with
   `--qps` where they are uncertain.

Wide-range scanning is a feature the project explicitly does not build.
A user who wants to do that has to fork or write their own.

## The user's responsibilities

By running `swoomps --scan`, the user agrees to:

1. **Respect local law.** Computer-misuse statutes vary by jurisdiction.
   Some countries treat unsolicited network probing as a civil or
   criminal offence even when the protocol is harmless. The user is
   responsible for knowing the law where they run.
2. **Respect the target's terms of service.** Many hosting providers
   explicitly prohibit scanning in their ToS or AUP. The user must
   review the ToS of any IP range they scan.
3. **Stay below the noise floor.** The default rate limit is 50 QPS
   across all workers. The user can and should lower it where they are
   uncertain.
4. **Honor opt-out.** Anyone who runs a Minecraft server can request to
   be excluded. See "Opt-out" below.
5. **Not publish the data.** The database is for personal use. Public
   release of harvested lists is a separate ethical question and is
   outside the scope of this project.

## Opt-out

Two opt-out mechanisms ship with the project:

1. **`blacklist.txt`** at `$XDG_CONFIG_HOME/swoomps/blacklist.txt`. One
   CIDR or IP per line. The scanner drops any target whose address is
   in this list before probing.
2. **DNS signal.** Operators of Minecraft servers who wish to opt out
   can publish a `TXT` record at `_swoomps.example.com` with value
   `opt-out`. The scanner checks this when resolving a host. (Future
   work; the MVP only implements the file-based blacklist.)

## Rate limiting

The default rate limit is 50 QPS, configurable downward via `--qps` or
`SWOOMPS_QPS`. The minimum is 1 QPS. The scanner will not exceed the
configured rate even when the worker pool is large.

In practice, the throughput bottleneck is TCP dial latency, not the
rate limiter. A scan with 64 workers and 50 QPS will hit roughly 50
successful probes per second when targets respond quickly, and many
more dial attempts that resolve to "no response" before the 3-second
timeout.

## Data handling

- The database is local. The scanner does not phone home.
- No analytics, no telemetry, no debug trace is sent externally.
- `log/slog` writes to stderr by default. The user can redirect.

## Legal background (informational, not legal advice)

The act of sending a TCP SYN and reading a TCP response is, in most
jurisdictions, a normal interaction on a public network. The legal
exposure comes from:

- Repetition designed to disrupt the target (denial of service).
- Bypassing authentication or access controls.
- Accessing systems the user is not authorised to access.

`swoomps` does none of these. The Minecraft protocols it speaks are
publicly documented and intended to be queried by any client. The
project's posture is that this is closer to a `dig` than to a port
scanner, but the user should still satisfy themselves that their use
case is lawful and aligned with the target's policies.

## Reporting

If a server operator wants to flag an issue with the scanner's
behaviour, the contact is the repository's issue tracker.
