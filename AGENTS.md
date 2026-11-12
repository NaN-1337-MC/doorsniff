# AGENTS.md — Regras absolutas deste projeto

## Regra de Segurança Git (OBRIGATÓRIA)

**ANTES de executar QUALQUER comando git destrutivo, você DEVE:**

1. **Parar tudo.**
2. **Explicar exatamente o que o comando faz.**
3. **Explicar exatamente o que será perdido/destruído.**
4. **Pedir confirmação explícita e direta do usuário.**
5. **O usuário DEVE confirmar explicitamente com "sim" ou "yes" PELOMENOS DUAS VEZES.**

**Comandos destrutivos incluem, mas não se limitam a:**
- `git rebase`
- `git reset --hard` / `git reset --mixed` / `git reset --soft`
- `git push --force` / `git push --force-with-lease`
- `git stash pop` que sobrescreveria trabalho não commitado
- `git checkout --` que descartaria mudanças
- Qualquer comando que delete ou sobrescreva código permanentemente

**Regra de confirmação em duas etapas:**
- Primeira confirmação: usuário diz "sim" ou "yes"
- Segunda confirmação: usuário digita o nome do comando completo ou uma palavra de segurança acordada

**Exemplo do que FAZER:**
```
Antes de: git reset --hard HEAD~3
Dizer: "Este comando vai apagar os últimos 3 commits permanentemente.
       Os cambios nesses commits serão perdidos.
       Confirme digitando 'sim':"
       [usuário digita "sim"]
       "Confirme novamente digitando 'sim, entendo que vou perder código':"
       [usuário digita "sim, entendo que vou perder código"]
       [agora sim executar]
```

## Feature: Lista Incremental de Players

Players em `sample_players` devem ser **acumulados**, não substituídos. Quando um servidor é re-probado, novos nomes de players devem ser **adicionados** à lista existente, não sobrescritos.

Ver `docs/players-incremental.md` para a especificação completa.

## Agent skills

### Issue tracker

Issues live as GitHub issues in `NaN-1337-MC/doorsniff`, driven by the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Five canonical triage roles, each label string equal to its name: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` at the repo root plus ADRs in `docs/adr/`. See `docs/agents/domain.md`.
