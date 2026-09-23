# interactivecodebase (`icb`)

`icb` statically analyzes a Go web codebase and lets you explore it top-down, in a web UI or through MCP for AI agents:

**pages → API/HTMX calls and static assets → handler call flow → sinks (SQL, file IO, 3rd-party calls) → code, types and DB schema**

See [docs/PLAN.md](docs/PLAN.md) for the design and the [roadmap issue](https://github.com/tewecske/interactivecodebase/issues/30) for progress.

> Status: early development. `analyze` and `query` build the call graph (functions, calls, interface dispatch,
> implementations), discover routes (net/http, chi, gin, echo, gorilla/mux), sinks (SQL via database/sql, sqlx, sqlc, pgx and
> gorm; files, HTTP, SMTP, exec, env), SQL tables from migrations
> route access levels, pages (templates, HTMX requests, assets), navigation between routes (links, forms,
> redirects) and route flows down to the tables they touch; the web UI and MCP are not implemented yet.

## Quickstart

Requires Go 1.26+.

```sh
make build            # builds bin/icb
bin/icb --help
bin/icb version
bin/icb analyze ../goweb       # analyze a module and print a summary
bin/icb serve ../goweb         # web UI, JSON API and MCP (at /mcp) on 127.0.0.1:8080
bin/icb serve -watch ../goweb  # ... and re-analyze when sources change
bin/icb mcp ../goweb           # MCP over stdio, for AI agents
```

Querying the graph from the terminal:

```sh
bin/icb query ../goweb routes
bin/icb query ../goweb page 'GET /{lang}/groups'            # templates, HTMX/form requests, assets
bin/icb query ../goweb callees 'route:GET /{lang}/home'      # where you can navigate from a page
bin/icb query ../goweb flow 'POST /{lang}/groups'           # call tree down to SQL and tables
bin/icb query -method GET ../goweb flow 'POST /{lang}/sign-in'
bin/icb query ../goweb callees 'groupHandler).create'
bin/icb query ../goweb callers 'Authenticator).Authenticate'
bin/icb query ../goweb paths 'groupHandler).create' 'GroupMembershipRepository).CreateGroupWithAdmin'
bin/icb query ../goweb search GroupRepository
bin/icb query -json ../goweb export > graph.json
```

A node argument is a node ID, an ID without its kind prefix, or text that matches exactly one node.

Or install it without the web UI (the API, CLI and MCP work; `/` explains how to add the UI):

```sh
go install github.com/tewecske/interactivecodebase/cmd/icb@latest
```

The web UI is built with Node 22+ by `make ui` (part of `make build`) and embedded into the binary.

## Configuration

An `icb.yaml` in the analyzed directory (or a file given with `-config`) tunes the analysis: which packages to
load, extra noise to leave out of flows, custom sinks (say, your own client for a third-party API, or an interface
whose implementation is wired in at deployment), authentication guards and role fields, migration directories, and
values for what cannot be resolved statically (route prefixes or names from configuration, `env:NAME`). Unknown
keys are errors. Editors that speak the YAML language server complete and check it with
[docs/icb.schema.json](docs/icb.schema.json); [examples/goweb/icb.yaml](examples/goweb/icb.yaml) is a worked
example. With `serve -watch`, editing `icb.yaml` re-analyzes too.

## JSON API

`icb serve` analyzes the module once (with `-watch`, again whenever `.go`, template or `.sql` files, `go.mod`
or the git HEAD change) and serves:

| Endpoint | |
|---|---|
| `/api/summary` | module, counts, timings |
| `/api/routes?access=&method=&q=` | routes with access level, handler, position |
| `/api/node?id=` | a node with its incoming and outgoing edges |
| `/api/page?route=` | templates, requests, assets and navigation of a page |
| `/api/flow?route=&method=&prune=` | a route's call tree down to sinks and tables |
| `/api/paths?from=&to=` | call paths between two nodes |
| `/api/source?file=&start=&end=` | source lines (confined to the module) |
| `/api/tables`, `/api/table?name=` | tables; a table's columns, foreign keys, queries and routes |
| `/api/search?q=&kind=` | full-text search over nodes |
| `/api/diagrams/{sitemap,flow,types,er}` | Mermaid source plus the graph IDs behind its nodes |
| `/api/status`, `/api/events` | analysis generation, progress and last error; `events` streams changes (SSE) |

A failed re-analysis (say, a file that does not compile) is reported while the last good analysis stays in
service; the UI shows the status in its header and reloads its data when a new analysis is ready.

`route` takes a route key such as `POST /{lang}/groups`. Requests need the access token (see below).

## gopls

If [gopls](https://go.dev/gopls) is installed (`go install golang.org/x/tools/gopls@latest`), `icb serve` and
`icb mcp` start it on first use: the code viewer shows its hover (type and docs) over identifiers and lists
references on right-click, and MCP gains `lsp_*` tools. Without gopls everything else works from the static index.

## Access and deployment

`icb serve` always requires a token: it prints a random one with a sign-in URL, or uses `-token` / `ICB_TOKEN`.
Browsers sign in once and get a cookie; API and MCP clients send `Authorization: Bearer <token>`. Serve HTTPS
with `-tls-cert`/`-tls-key`, or keep the loopback default behind Tailscale or a TLS proxy. A Docker image and a
compose file are included. See [docs/remote.md](docs/remote.md).

## MCP

`icb mcp <dir>` serves the Model Context Protocol over stdio; `icb serve` also serves it over streamable HTTP at
`/mcp`. For Claude Code:

```sh
claude mcp add icb -- /path/to/icb mcp /path/to/your/module
```

Tools: `list_routes`, `get_route`, `get_flow` (text or Mermaid), `get_node`, `get_source`, `find_callers`,
`find_callees`, `find_paths`, `routes_touching_table`, `list_tables`, `get_table` (text or Mermaid), `search`,
`reanalyze`, and with gopls installed `lsp_hover`, `lsp_references`, `lsp_implementations`. Resources: `icb://routes`, `icb://schema`. See [docs/mcp.md](docs/mcp.md) for setup, IDs and examples.

## Development

```sh
make check   # gofmt check, go vet, golangci-lint, tests (with -race), library fixtures (without)
make test
make lint    # runs a pinned golangci-lint via `go run`, no install needed
make ui      # builds and tests the web UI (ui/, Vite + React + Mermaid) into internal/webui/dist
cd ui && npm run dev   # UI dev server on :5173, proxying /api to a running icb serve
make test-goweb  # checks testdata/golden/goweb.json against ../goweb (or $ICB_GOWEB_DIR) at the pinned commit
```

### Fixtures

- `testdata/fixtures/webapp/`: a small stdlib-only web app covering the patterns analysis must handle, with `expected.json`.
- `testdata/fixtures/routers/{chi,gin,echo,gorillamux}/`: one small module per router library (prefixes, groups,
  mounts, middleware) with its golden `routes.json`.
- `testdata/fixtures/data/{sqlx,sqlc,gorm,pgx}/`: one small module per data-access library with the SQL sinks and
  tables it should find in `sinks.json`.
- `testdata/golden/goweb.json`: expectations for [goweb](https://github.com/tewecske/goweb) at a pinned commit.

`internal/fixture` loads both and, until the analyzers exist, checks that every function, type, table, foreign key, template and asset the expectations name really exists.

CI runs the same checks on every push to `main` and on pull requests.
