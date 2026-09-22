# interactivecodebase (`icb`)

`icb` statically analyzes a Go web codebase and lets you explore it top-down, in a web UI or through MCP for AI agents:

**pages → API/HTMX calls and static assets → handler call flow → sinks (SQL, file IO, 3rd-party calls) → code, types and DB schema**

See [docs/PLAN.md](docs/PLAN.md) for the design and the [roadmap issue](https://github.com/tewecske/interactivecodebase/issues/30) for progress.

> Status: early development. `analyze` and `query` build the call graph (functions, calls, interface dispatch,
> implementations), discover net/http routes, sinks (SQL, files, HTTP, SMTP, exec, env), SQL tables from migrations
> route access levels, pages (templates, HTMX requests, assets), navigation between routes (links, forms,
> redirects) and route flows down to the tables they touch; the web UI and MCP are not implemented yet.

## Quickstart

Requires Go 1.26+.

```sh
make build            # builds bin/icb
bin/icb --help
bin/icb version
bin/icb analyze ../goweb       # analyze a module and print a summary
bin/icb serve ../goweb         # web UI + JSON API + MCP on 127.0.0.1:8080 (not yet)
bin/icb mcp ../goweb           # MCP over stdio, for AI agents (not yet)
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

Or install it:

```sh
go install github.com/tewecske/interactivecodebase/cmd/icb@latest
```

## Development

```sh
make check   # gofmt check, go vet, golangci-lint, tests (with -race)
make test
make lint    # runs a pinned golangci-lint via `go run`, no install needed
make ui      # builds the web UI once ui/ exists
make test-goweb  # checks testdata/golden/goweb.json against ../goweb (or $ICB_GOWEB_DIR) at the pinned commit
```

### Fixtures

- `testdata/fixtures/webapp/`: a small stdlib-only web app covering the patterns analysis must handle, with `expected.json`.
- `testdata/golden/goweb.json`: expectations for [goweb](https://github.com/tewecske/goweb) at a pinned commit.

`internal/fixture` loads both and, until the analyzers exist, checks that every function, type, table, foreign key, template and asset the expectations name really exists.

CI runs the same checks on every push to `main` and on pull requests.
