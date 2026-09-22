# interactivecodebase — Project Plan

A tool that statically analyzes a Go web codebase and lets humans (web UI) and AI agents (MCP) explore it
top-down: **pages → API calls/static assets → handler call flow → sinks (SQL / file IO / 3rd-party calls) →
code, types and DB schema**.

Reference target: `../goweb` (net/http `ServeMux`, HTMX + `html/template`, hexagonal services, PostgreSQL via
`database/sql` + pgx, `embed.FS` assets, SQL migrations).

## 1. User-facing experience

1. **Site map** – every URL a user can reach, grouped/coloured by access level
   (`public`, `authenticated`, `admin`, `guest`), with edges showing *navigation* ("from page A you can get to B"
   via links, forms, redirects, HTMX calls).
2. **Page drill-down** – click a URL → list of API/HTMX calls the page makes and the static resources it loads.
3. **Flow diagram** – click an API call → handler file/func, and a sequence diagram of the calls down to terminal
   sinks: SQL query, file read/write, outbound HTTP, SMTP, exec, env/config.
4. **Node detail** – click any node → source code (syntax highlighted, go-to-definition), involved
   structs/interfaces/types, interface → implementation links; for SQL nodes: the query, tables touched,
   columns, operation (SELECT/INSERT/…) and an ER view of related tables.
5. **MCP server** – the same graph queryable by AI agents ("which routes write to `sessions`?",
   "what's the call path from `POST /{lang}/groups` to SQL?").
6. **Remote access** – served over HTTP(S) with authentication so it can be used from anywhere.

All diagrams are **Mermaid**, generated server-side in Go so the UI and MCP share the same output:

| View | Mermaid diagram |
|---|---|
| Site map | `flowchart` with one `subgraph` per access level |
| Flow | `sequenceDiagram` (participants = handler/services/repos/DB; `alt`/`opt` for branches) |
| Node detail | `classDiagram` of directly involved types, interface ↔ implementation |
| SQL | `erDiagram` of a table and its FK neighbours |

## 2. Architecture

```
            ┌──────────────── icb analyze ────────────────┐
 Go module ─┤ go/packages → SSA → callgraph (VTA)         │
            │   ├─ route discovery (ServeMux/chi/gin/...) │
            │   ├─ auth classifier                        │──► in-memory SQLite
 templates ─┤   ├─ template/HTMX/static analyzer          │         │
 migrations ┤   ├─ sink detector (SQL/file/http/smtp/exec)│         │
            │   └─ SQL extractor + schema (migrations)    │         │
            └─────────────────────────────────────────────┘         │
                                                                     ▼
                             icb serve ── JSON API + Mermaid ── Web UI (SPA, embedded)
                                      └─ MCP (streamable HTTP)   icb mcp (stdio)
                                      └─ gopls bridge (hover/definition/references)
```

Single Go binary `icb`, subcommands `analyze`, `serve`, `mcp`, `query`.

### Key technical decisions (defaults — open to change)

| Concern | Choice | Why |
|---|---|---|
| Loading/type info | `golang.org/x/tools/go/packages` + `go/types` | Precise, batch-friendly |
| Call graph | `x/tools/go/ssa` + `callgraph/vta` (fallback CHA) | Resolves interface calls (`service.GroupRepository` → `postgres.GroupRepository`) with far less noise than CHA |
| Dynamic route strings | SSA constant propagation + bounded loop unrolling over constant slices/funcs (e.g. `locale.Codes()`) | goweb registers routes as `"GET "+prefix+"/groups"` inside a language loop |
| LSP | `gopls` (spawned per workspace) for live hover/definition/references in the code viewer | User asked for LSP; static index stays the source of truth for the graph |
| SQL parsing | `pganalyze/pg_query_go` (Postgres grammar); pluggable dialect | Accurate table/column extraction; migrations define schema + FKs |
| Storage | **In-memory** SQLite (`modernc.org/sqlite`, pure Go), used directly (no storage interface): `nodes`, `edges`, `symbols`, `sql_tables`, `sql_columns`, `fk`, FTS index. Analysis runs at startup / on re-analysis | Simple; graph is small (~tens of thousands of nodes). Revisit Postgres (+ Apache AGE for Cypher) only if central multi-repo hosting is needed |
| Web UI | TypeScript + Vite + React, **Mermaid** for all diagrams, **Shiki** for code | Keep it simple; Mermaid covers sequence/class/ER/flowchart, supports click callbacks, and the same text is useful to AI agents via MCP |
| MCP | `github.com/modelcontextprotocol/go-sdk` | Official Go SDK; stdio + streamable HTTP |
| Remote access | Bearer-token/basic auth (on by default), optional TLS, Docker image; recommend Tailscale/reverse proxy | Tool exposes source code → never unauthenticated |

## 3. Graph model

Node kinds: `route`, `page`, `htmx_call`, `static_asset`, `template`, `handler`, `func`, `method`, `interface_call`,
`sink.sql`, `sink.file`, `sink.http`, `sink.smtp`, `sink.exec`, `type`, `sql_table`, `sql_column`.

Edge kinds: `navigates_to` (link/form/redirect), `requests` (page → HTMX/API call), `loads` (page → static),
`handled_by`, `calls`, `implements`, `dispatches_to` (interface → concrete), `renders` (handler → template),
`uses_type`, `queries` (sink.sql → table, with op), `fk` (table → table), `guarded_by` (route → auth guard).

Every node carries `pos` (file:line:col range) so the UI/MCP can always show code.

## 4. Analysis details & known hard parts

- **Route discovery**: find calls to `(*http.ServeMux).Handle/HandleFunc` (Go 1.22 method+pattern syntax),
  chi/gin/echo/gorilla later via detector plugins. Evaluate pattern args by SSA constant folding; unroll loops over
  constant-returning funcs; closures wrapping a method (`func(w,r){ auth.oauthStart(w,r,p) }`) resolved to the inner call.
  Unresolvable parts become `{?}` placeholders rather than being dropped.
- **Auth classification**: goweb checks auth *inside* handlers. Configurable "guard" functions
  (`Authenticator.Authenticate`, `adminHandler.authorize`, middleware wrappers). A route is `authenticated` if every
  path to a sink passes through a guard (post-dominance on the handler CFG); `public` otherwise; `mixed` if both
  (e.g. sign-in GET redirects authenticated users). Heuristic auto-detection + `icb.yaml` overrides.
- **Navigation graph**: parse templates (`text/template/parse`) for `href`, `action`, `hx-get/post/...`,
  `<script src>`, `<link href>`; resolve `{{.CreateURL}}` by tracing which struct field is set in the handler's
  view-model (SSA store to field → constant/format string). Plus `http.Redirect` targets from handlers.
- **Sinks**: `database/sql`, `pgx`, `sqlx`, `os.*File*`, `io/fs`/`embed.FS`, `net/http.Client`, `net/smtp`,
  `os/exec`, `os.Getenv`. Configurable list.
- **SQL**: fold the query arg to a constant (`groupSelect+" WHERE id = $1"`); parse; map to tables/columns and
  operation. Build schema from `migrations/*.up.sql` (CREATE/ALTER TABLE, FKs). Dynamic SQL → marked partial.
- **Flow pruning**: hide stdlib/logging/tracing noise by default (configurable filters), collapse trivial wrappers.

## 5. Milestones

- **M0 Foundation** – repo skeleton, CLI, graph schema/store, fixtures incl. goweb golden test.
- **M1 Static analysis core** – loader/SSA/callgraph, routes, flows, sinks, SQL+schema, auth, templates/navigation.
- **M2 API & Web UI** – JSON API, site map, page drill-down, flow diagram, code/type view, SQL/ER view, search.
- **M3 MCP server** – tools/resources over the graph, stdio + HTTP.
- **M4 Remote & live** – auth, TLS, Docker/systemd deploy, gopls bridge, watch mode/re-analysis.
- **M5 Extensibility** – router detectors (chi/gin/echo/gorilla), ORM/sql libs (sqlx/gorm/sqlc), `icb.yaml` rules, gRPC/queue entry points.

Success criterion for M1–M3: on goweb, all routes in `internal/adapter/http/handler.go` are discovered with
correct auth level, and e.g. `POST /en/groups` shows a flow down to `INSERT INTO groups` with the `groups` table
and its FKs.
