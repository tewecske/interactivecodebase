# interactivecodebase (`icb`)

`icb` statically analyzes a Go web codebase (and, in progress, Scala 3 sbt projects) and lets you explore it top-down, in a web UI or through MCP for AI agents:

**pages → API/HTMX calls and static assets → handler call flow → sinks (SQL, file IO, 3rd-party calls) → code, types and DB schema**

See [docs/PLAN.md](docs/PLAN.md) for the design and the [roadmap issue](https://github.com/tewecske/interactivecodebase/issues/30) for progress.

> Status: early development. `analyze` and `query` build the call graph (functions, calls, interface dispatch,
> implementations), discover routes (net/http, chi, gin, echo, gorilla/mux) and other entry points (workers
> started from main and their jobs, cobra commands, gRPC services, NATS/Pub/Sub consumers), sinks (SQL via database/sql, sqlx, sqlc, pgx and
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
bin/icb query ../goweb entries                             # workers and their jobs, commands, gRPC, consumers
bin/icb query ../goweb flow 'entry:job token_retention'     # what a background job touches
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
The export format is described by [docs/graph.schema.json](docs/graph.schema.json); icb imports the
same format from extractors for other languages.

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

## Scala (sbt) projects

A directory with a `build.sbt` and no `go.mod` is analyzed as a Scala 3 sbt project (`lang: scala` in `icb.yaml`
forces it). It needs a JDK, sbt and the extractor in [extractors/scala](extractors/scala), which reads the
compiled TASTy with the Scala 3 TASTy Inspector and writes the code graph in the
[docs/graph.schema.json](docs/graph.schema.json) format:

```sh
make scala-extractor                                   # writes extractors/scala/target/icb-scala
export ICB_SCALA=$PWD/extractors/scala/target/icb-scala  # or scala.extractor in icb.yaml, or icb-scala on PATH
bin/icb analyze ../gathedge
```

icb runs `sbt -batch -error -J-Xmx1500m "export Compile/fullClasspath"` in the project (or
`export <p>/Compile/fullClasspath` for each of `scala.projects`), which compiles it and prints each project's
classpath. Directories inside the project are its own classes; everything else is a library, read only to
resolve types. A project whose classes another one already includes (a `shared` module a `backend` depends on)
is skipped. The extractor then runs once with every project (`icb-scala --root DIR -o FILE [--guard NAME=ROLE]...
(--classpath CP CLASSDIR...)...`, heap capped at 1500 MB; `ICB_SCALA_JAVA_OPTS` replaces the JVM options).

The graph has a `func` node per def or val of an object or package (`func:pkg.Obj.name`), a `method` node per
def or val of a class or trait (`method:(pkg.Class).name`), `calls` edges (lambdas and local defs count towards
the enclosing def), calls of abstract methods as `interface_call` nodes that `dispatches_to` the implementations
(class-hierarchy analysis), and `type` nodes (`class`, `trait`, `object`, `enum`) with `implements` and
`uses_type` edges for the types diagram. Calls into libraries and compiler-generated members are left out.
TASTy is written before `inline def`s are inlined, so their call sites are kept; only `transparent inline` defs
and macros such as Skunk's `sql` are already expanded.

zio-http routes become `route` nodes like Go's. A small evaluator reads the `Routes` and `Route` vals and defs:
`Method.GET / "api" / notes / long("id") -> handler(...)` (string literals and vals; `string`/`long`/`int`/`uuid`
parameters become `{id}`, `trailing` `{trailing...}`, anything it cannot evaluate `{?}`), `Routes(...)`, `++`,
`@@` aspects, and `Endpoint`s (also those defined in a shared module) with `implement*`, including `Endpoint`s
built from method and path templates such as gathedge's `ApiPath1[Long](GET, "/api/words/{id}")`. The served
routes are those of the `Routes` values no other one includes. Aspects applied there are `muxMiddleware`, those
applied inside are the route's `middleware`, both outermost first. A handler that only calls a def is that def;
any other handler expression gets its own node named after its member, numbered in source order
(`func:app.NoteRoutes.routes$1`), with the calls it makes (the member keeps the rest). Access comes from the
aspects: `admin*` means admin, `authenticat*`/`requireAuth`/`requireUser` authenticated, `optionalUser` a public
route that consults the session; `auth.guards` in `icb.yaml` names others by their Scala name
(`app.http.RouteSupport.staffOnly`, or a suffix such as `RouteSupport.staffOnly`) with their role.

Sinks become `sink.*` nodes like Go's, called from the def or route handler they are written in: SQL from Quill's
`ctx.run`, Skunk's `sql"..."` (arguments as `$1`, `$2`, ...) and JDBC (`prepareStatement`, `executeQuery`, ...);
zio-http's `Client`, sttp and `java.net.http` (`sink.http`); `java.nio.file.Files`, `scala.io.Source` and
`java.io` streams (`sink.file`); `sys.env`, `System.getenv`, `zio.System.env*` and `ZIO.config` (`sink.env`);
Jakarta/javax Mail's `Transport.send` (`sink.smtp`). TASTy holds a Quill query before Quill's macros turn it into
SQL, so the extractor reads the quoted Scala, following the vals and `inline def`s it uses: `querySchema[T]("t",
_.field -> "col")` and `query[T]` (the naming strategy of the context's type, snake_case by default) are tables,
`insertValue`/`insert`/`updateValue`/`update`/`delete` the statement and its target, fields of a table's row type
the columns. It writes that as SQL (`SELECT t0.id, t1.email FROM sessions t0, users t1`, `UPDATE users t0 SET
email = $1 WHERE t0.id = $2`) for icb to parse. On import icb loads the migrations (`migrations` in `icb.yaml`,
else the usual directories and Flyway's `src/main/resources/db/migration` of each module, or the PostgreSQL one of
its per-database subdirectories, applied in version order) and links each SQL sink to the tables and columns it
touches, as for Go.

Calls made in the operands of ZIO's parallel combinators (`<&>`, `<&`, `&>`, `zipPar*`, `zipWithPar`,
`collectAllPar`, `foreachPar`, ...) carry `parallel` (the combinator's position, shared by the calls that run
alongside each other) and `branch` (the operand's number, or `each` for a function run once per element); an
operand that is a local val, such as a for comprehension's `b = repo.find(id)`, marks the calls of its definition.
Flows keep a group's calls together, the sequence diagram draws them as a `par` block with one section per
branch, and the flow tree marks them `∥1`, `∥2`, ...

A Laminar frontend (a Scala.js project, read with its own classpath) routed by Waypoint adds `page` nodes: the
backend serves it as a single-page app, so its pages are not server routes. Each path of a Waypoint route
(`Route.static(SignIn, root / "sign-in", basePath)`, `Route(encode, decode, pattern = root / "g" / segment[String],
basePath)`, `Route.withQuery` ...) is a page, `page:/{basePath}/g/{slug}`: a path parameter is named after the page
class's field of its type, a base path known only at run time after the value holding it, and `? params` queries
are left out. A page has the attributes of a GET route (`method`, `pattern`, `handler`, `access` public) plus
`page`, its page classes, and is `handled_by` the views rendering it: the defs returning a Laminar element called
from a `case` matching the page class or from the renderer of a SplitRender `collect*` for it. The requests its
views make, directly or through what they call, are `htmx_call` nodes like Go's (`trigger` fetch, `via` the def
making it) that the page `requests`: every use of a shared path template (such as gathedge's `ApiPath`) or zio-http
`Endpoint`, so the method and path are those of the backend route exactly, which it is `handled_by` (a route
differing only in the names of path parameters counts; a request no route serves has no `target`).
`router.navigateTo(page)` and `relativeUrlForPage` (trigger link), `pushState` (navigate) and `replaceState`
(redirect) are `navigates_to` edges to the pages of the page's class, found by its type; a def that only passes its
page parameter on, such as a navigation link helper, is followed to its callers. A route whose `matchEncode` is
`PartialFunction.empty` only decodes URLs and is never a link target. The site map draws pages with the routes, and
the route view (`/api/page?route=page:/...`) shows a page's views, requests and navigation.

How sbt runs is `scala.server` in `icb.yaml`. By default (`reuse`) icb runs it in batch mode, a JVM of its own
(about 10 s on gathedge with the build compiled), unless an sbt server is already running in the project, such as
an open sbt shell or the one Metals starts: then it runs `sbt --client` in that server, which is quicker and does
not compete with it over `target/`. A client that cannot reach the server falls back to batch mode. `start` also
starts a server when none runs (with `SBT_OPTS=-Xmx1500m`) and leaves it running; `off` always uses batch mode.

`serve -watch` re-analyzes a Scala project when a `.scala` or `.sbt` file, `project/build.properties`, a `.sql`
migration, `icb.yaml` or the git HEAD changes, leaving out `target/` directories, test sources (`src/test`,
`src/it`), dot directories (`.bsp`, `.metals`, `.bloop`, `.git`) and `node_modules`. Saves are debounced: the
analysis runs once changes have settled for a second, and changes made while it runs trigger one more. Unless
`scala.server` says otherwise it runs sbt as `start` does, so each re-analysis costs an incremental compile in a
warm server plus the extractor: about 15 s on gathedge (4 s on the zioapp fixture), against about 23 s for a cold
`icb analyze`. The server holds about 2.5 GB of memory on gathedge while icb runs; a server icb started is shut down
when `icb serve` stops (one that was already running is left alone). A failed compile or extraction keeps the last
good analysis in service and shows the error in the UI.

## JSON API

`icb serve` analyzes the module once (with `-watch`, again whenever `.go`, template or `.sql` files, `go.mod`
or the git HEAD change; for Scala see above) and serves:

| Endpoint | |
|---|---|
| `/api/summary` | module, counts, timings |
| `/api/routes?access=&method=&q=&pages=` | routes with access level, handler, position; `pages=1` adds frontend pages |
| `/api/entries` | other entry points: workers, jobs, commands, gRPC methods, consumers |
| `/api/node?id=` | a node with its incoming and outgoing edges |
| `/api/page?route=` | templates, requests, assets and navigation of a route's page or a frontend page (`page:` ID) |
| `/api/flow?route=&method=&prune=` | a route's call tree down to sinks and tables |
| `/api/paths?from=&to=` | call paths between two nodes |
| `/api/source?file=&start=&end=` | source lines (confined to the module) |
| `/api/tables`, `/api/table?name=` | tables; a table's columns, foreign keys, queries and routes |
| `/api/search?q=&kind=` | full-text search over nodes |
| `/api/diagrams/{sitemap,flow,types,er}` | Mermaid source plus the graph IDs behind its nodes |
| `/api/status`, `/api/events` | analysis generation, progress and last error; `events` streams changes (SSE) |

A failed re-analysis (say, a file that does not compile) is reported while the last good analysis stays in
service; the UI shows the status in its header and reloads its data when a new analysis is ready.

`route` takes a route key such as `POST /{lang}/groups`, or an entry point ID such as `entry:job token_retention`. Requests need the access token (see below).

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

Tools: `list_routes`, `list_entry_points`, `get_route`, `get_flow` (text or Mermaid), `get_node`, `get_source`, `find_callers`,
`find_callees`, `find_paths`, `routes_touching_table`, `list_tables`, `get_table` (text or Mermaid), `search`,
`reanalyze`, and with gopls installed `lsp_hover`, `lsp_references`, `lsp_implementations`. Resources: `icb://routes`, `icb://schema`. See [docs/mcp.md](docs/mcp.md) for setup, IDs and examples.

## Development

```sh
make check   # gofmt check, go vet, golangci-lint, tests (with -race), library fixtures (without)
make test
make lint    # runs a pinned golangci-lint via `go run`, no install needed
make ui      # builds and tests the web UI (ui/, Vite + React + Mermaid) into internal/webui/dist
cd ui && npm run dev   # UI dev server on :5173, proxying /api to a running icb serve
make test-scala  # sbt test of the Scala extractor, then the zioapp fixture through sbt (part of make check; needs sbt and a JDK)
make test-goweb  # checks testdata/golden/goweb.json against ../goweb (or $ICB_GOWEB_DIR) at the pinned commit
make test-gathedge  # checks testdata/golden/gathedge.json against ../gathedge (or $ICB_GATHEDGE_DIR) at the pinned commit
```

`make test-gathedge` builds the extractor and analyzes a [gathedge](https://github.com/tewecske/gathedge) checkout
through sbt, which writes only its `target/` directories; it takes about 30 s with the build compiled and about
1 GB of memory. The checkout is `$ICB_GATHEDGE_DIR`, else `gathedge` next to this repository or next to its parent
directory; the test is skipped if it is missing or at another commit (`ICB_GATHEDGE_REQUIRED=1` fails instead).
After changing the analysis on purpose, `ICB_GATHEDGE_UPDATE=1 make test-gathedge` rewrites the golden; review
its diff.

### Fixtures

- `testdata/fixtures/webapp/`: a small stdlib-only web app covering the patterns analysis must handle, with `expected.json`.
- `testdata/fixtures/routers/{chi,gin,echo,gorillamux}/`: one small module per router library (prefixes, groups,
  mounts, middleware) with its golden `routes.json`.
- `testdata/fixtures/data/{sqlx,sqlc,gorm,pgx}/`: one small module per data-access library with the SQL sinks and
  tables it should find in `sinks.json`.
- `testdata/fixtures/entrypoints/`: commands, a worker with a job table, gRPC and NATS (stub modules via `replace`),
  with its golden `entries.json`.
- `testdata/fixtures/scala/zioapp/`: a small sbt Scala 3 app shaped like gathedge (zio-http routes with aspects,
  shared `Endpoint`s and path templates, a service trait and implementation, a Quill repository with a Flyway
  migration, env and HTTP calls, a shared module cross-built for Scala.js, a Laminar and Waypoint frontend), for
  the Scala extractor; `routes.json` holds its expected routes, `sinks.json` its sinks with the tables they touch
  and `pages.json` its frontend pages with their views, requests and navigation.
- `testdata/golden/goweb.json`: expectations for [goweb](https://github.com/tewecske/goweb) at a pinned commit.
- `testdata/golden/gathedge.json`: facts about gathedge at a pinned commit that do not depend on positions or
  generated code: node counts of routes, pages, requests, tables, columns and SQL sinks; every `/api` route with its
  access and handler; each page with its first view and the routes it requests; where a few pages link and which
  pages nothing links to; and the tables (with operations) the flows of a few routes reach.

`internal/fixture` loads both and, until the analyzers exist, checks that every function, type, table, foreign key, template and asset the expectations name really exists.

CI runs the same checks on every push to `main` and on pull requests.
