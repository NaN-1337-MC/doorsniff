# Incremental Player List — Especificação

## Problema

Quando um servidor é re-probado, a lista de `sample_players` é **substituída** inteira em vez de acumulada. Isso significa que players históricos se perdem.

## Comportamento Desejado

1. Novos players devem ser **adicionados** à lista existente
2. Players já conhecidos não devem ser duplicados
3. A lista deve preservar a ordem de descoberta (players mais antigos primeiro)
4. `sample_players` no SQLite continua sendo um JSON array

## Implementação Proposta

### Storage Layer (`repo.go`)

O upsert precisa fazer merge ao invés de substituir. Opções:

**Opção A: Merge no código Go antes de inserir**
```go
// Antes de fazer upsert, buscar lista existente e fazer merge
existing, _ := repo.GetServer(ctx, s.IP, s.Port)
merged := mergePlayers(existing.SamplePlayers, s.SamplePlayers)
s.SamplePlayers = merged
```

**Opção B: SQL com json_each + json_insert**
```sql
-- Não suportado nativamente para arrays JSON no SQLite
-- Alternativa: usar uma tabela separada
```

### Modelo de Dados

**Tabela `server_players` (nova):**
```sql
CREATE TABLE server_players (
  server_ip   TEXT NOT NULL,
  server_port INTEGER NOT NULL,
  player_name TEXT NOT NULL,
  first_seen  TEXT NOT NULL,
  last_seen   TEXT NOT NULL,
  PRIMARY KEY (server_ip, server_port, player_name)
);
```

**Coluna `sample_players` continua existindo?** Sim, como cache/denormalização para queries rápidas.

### Fluxo

1. Server é probado → novos `SamplePlayers` recebidos
2. Para cada player novo (não existente na `server_players`):
   - `INSERT OR IGNORE INTO server_players (...)`
3. `sample_players` é reconstruído do banco (todos os players únicos ordernados por `first_seen`)
4. Upsert com o JSON reconstruído

### Questões em Aberto

- [ ] Limite de players por server? (ex: max 500 por server)
- [ ] Cleanup de players que não são mais vistos há muito tempo?
- [ ] `player_names` do Query também entra na acumulação?
