# Using icb from an AI agent (MCP)

`icb` speaks the [Model Context Protocol](https://modelcontextprotocol.io), so an agent can ask it about a Go web
application: which URLs exist and who may call them, what an endpoint does down to its SQL, who writes a table,
how two pieces of code connect.

## Setup

**stdio** (the agent starts `icb`):

```sh
# Claude Code
claude mcp add icb -- /path/to/icb mcp /path/to/your/module

# any client that takes a command
/path/to/icb mcp /path/to/your/module
```

`icb mcp` analyzes the module once at start-up (a few seconds) and then answers from memory. Stdout carries only the
protocol; errors go to stderr.

**Streamable HTTP** (one shared server): `icb serve /path/to/module` serves MCP at `http://127.0.0.1:8080/mcp`,
next to the web UI and JSON API.

```sh
claude mcp add --transport http icb http://127.0.0.1:8080/mcp --header "Authorization: Bearer $ICB_TOKEN"
```

`icb serve` always requires a token (printed at start, or set with `ICB_TOKEN`); see [remote.md](remote.md).

## Referring to things

Every answer names graph IDs, which any tool accepts back:

| Thing | ID |
|---|---|
| route | `route:POST /{lang}/groups` (or just `POST /{lang}/groups`) |
| function | `func:example.com/app/web.health` |
| method | `method:(*example.com/app/web.Handler).List` |
| interface method | `interface_call:(example.com/app/service.Repository).Save` |
| type | `type:example.com/app/service.Group` |
| table | `sql_table:groups` |
| SQL call site | `sink.sql:internal/store/postgres/groups.go:409:37` |

Tools also accept a name that matches one node (`GroupService).Create`); an ambiguous name returns the candidates.
Routes registered in a loop over languages appear once, as `/{lang}/…`; parts that depend on runtime
configuration show as `{?}`.

## Tools

| Tool | Answers |
|---|---|
| `list_routes` | Every URL: method, pattern, access (`public`, `authenticated`, `admin`, `guest`; `*` = public but reads the session), handler. Filter by `access`, `method`, `match`. |
| `get_route` | One route: access and the guard that enforces it, handler, middleware, templates, the requests and assets its page uses, navigation to and from it. |
| `get_flow` | What a route does: its call tree through services and interface dispatch down to SQL (with tables), files, outbound HTTP, mail, processes and env reads. `format: "mermaid"` gives a sequence diagram; `prune` shows more; `method` follows another request method's branches. |
| `get_node` | Any node: kind, package, source range, detail (signature, SQL, URL), edges in and out. |
| `get_source` | Source lines of a module file. |
| `find_callers` / `find_callees` | Direct callers or callees, including interface dispatch and routes. |
| `find_paths` | Shortest paths between two nodes, e.g. a route and a table. |
| `routes_touching_table` | Routes whose flows read or write a table, with the operation. |
| `list_tables` / `get_table` | Tables from the migrations with columns; one table's keys, foreign keys both ways with `ON DELETE`, queries and routes. `format: "mermaid"` gives an ER diagram. |
| `search` | Full-text search over names, packages, files, SQL text and IDs; `kind` narrows it (`route`, `method`, `sql_table`, `sink.sql`, `sink.env`, …). |
| `reanalyze` | Analyze again after the code changed; the previous analysis stays if the code no longer builds. |

Resources: `icb://routes` (the route list) and `icb://schema` (tables plus a Mermaid ER diagram).

## Example questions

These are checked on every change, against the fixture app (`testdata/fixtures/webapp/mcp-scenarios.json`, part of
`make check`) and against goweb (`testdata/golden/goweb-mcp-scenarios.json`, part of `make test-goweb`). Each
scenario is a question, the tool call that answers it, and text the answer must and must not contain:

- *Which routes write to the sessions table?* → `routes_touching_table {table: sessions, op: insert}`: sign-in,
  sign-up, guest sign-in and the OAuth callback.
- *Is `POST /{lang}/groups/{id}/rename` authenticated, and where is it checked?* → `get_route`:
  `authenticated via (*http.groupHandler).authenticatedUser`.
- *What does creating a group write?* → `get_flow {route: POST /{lang}/groups}`: `INSERT INTO groups` and
  `group_members`.
- *How does `POST /{lang}/groups` reach `group_members`?* → `find_paths`: through `GroupService.Create` and the
  `GroupMembershipRepository` interface.

To add one, append an entry to a scenario file; the runner is `internal/mcpserver/scenarios_test.go`.

## Limits

Answers come from static analysis. Calls through reflection, plugins or code generated at run time are not seen;
values built at run time show as `{?}`; access levels are inferred from how handlers check the session (each
answer shows its evidence). Lists are capped (200 lines, 40 edges per kind, 300 source lines, 5 paths) to fit an
agent's context; narrow the query for more.
