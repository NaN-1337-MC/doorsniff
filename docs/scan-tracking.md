# Histórico Completo de Presença (online + ausente) — Especificação

## Problema

`player_events` só registra linhas para players **vistos online**. Se um scan acontece e o player não está online, não há registro — então o usuário não consegue distinguir:

- "player nunca esteve nesse servidor"
- "player não estava online nesse scan em particular"

## Solução

Nova tabela `server_scans` (schema V8) registra **toda vez** que um servidor foi escaneado e respondeu com dados. Combinada com `player_events`, permite reconstruir:

> Em cada scan onde esse server respondeu, esse player estava online ou ausente?

## Schema

```sql
CREATE TABLE server_scans (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  server_ip    TEXT    NOT NULL,
  server_port  INTEGER NOT NULL,
  scanned_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  had_response INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_server_scans_lookup
  ON server_scans(server_ip, server_port, scanned_at DESC);

CREATE INDEX idx_server_scans_recent
  ON server_scans(scanned_at DESC)
  WHERE had_response = 1;
```

- `had_response = 1` → SLP/legacy ping respondeu com qualquer dado (mesmo que lista de players vazia)
- `had_response = 0` → probe falhou (TCP timeout / RST). Schema reserva esse valor, mas a versão `indev` só registra scans bem-sucedidos.
- `idx_server_scans_recent` é um partial index (`WHERE had_response = 1`) — acelera queries de "última vez que esse server respondeu".

## Onde é gravado

| Comando | Caminho de sucesso | SLP respondendo? |
|---|---|---|
| `probe` | `scanner.Run` → `pool.run` → `recordServerScan(true)` | ✅ |
| `update` | `update.go` linha ~232 → `repo.RecordServerScan(...)` | ✅ |

Ambas as chamadas são gateadas pelo flag `--playerhistory` (default `true`).

## Como consultar

```bash
doorsniff list server 103.15.236.134:25565
```

Mostra uma linha por scan (DESC):

```
SEEN                 RESPONSE
28/07/2026 20:20:14  YES
28/07/2026 18:13:12  YES
```

Com `--player Notch`:

```
SEEN                 STATUS         PLAYER
28/07/2026 20:20:14  ONLINE         Notch
28/07/2026 18:13:12  ABSENT         Notch
```

STATUS é determinado por:

1. `had_response == 0` → `NO_RESPONSE`
2. `had_response == 1` e `scanned_at` corresponde a uma row em `player_events` para esse player → `ONLINE`
3. `had_response == 1` e sem row em `player_events` → `ABSENT`

## Limitações Conhecidas

- **Apenas scans de sucesso** são registrados. Portas fechadas/timeouts ainda não geram row (campo `had_response` reservado para uso futuro).
- **Joins entre scanner e banco**: o scan é registrado **depois** do upsert da `servers` (no path SLP), então há uma pequena janela onde o scan é registrado mas o player_event não (perda rara, não significativa).
- **Sem batch**: cada scan vira 1 INSERT. Em 200 qps durante 1h isso é ~720k inserts. Aceitável pra `indev`.

## Workarounds

- `list player Notch` continua mostrando só `player_events` (apenas appearances online). Se quiser ver os scans onde o player **não estava** online, use `list server <ip> --player Notch`.
