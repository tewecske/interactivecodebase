// Renders the app shell against a mocked API.
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";

vi.mock("mermaid", () => ({
  default: { initialize: vi.fn(), render: vi.fn(async () => ({ svg: '<svg><g id="flowchart-r0-0"></g></svg>' })) },
}));

const responses: Record<string, unknown> = {
  "api/summary": {
    module: "example.com/webapp",
    dir: "/src/webapp",
    packages: 11,
    moduleFunctions: 46,
    counts: { nodes: { route: 13, sql_table: 4, func: 47, method: 51 }, edges: { navigates_to: 24 } },
    migrationDirs: ["migrations"],
    analysisMs: 2500,
  },
  "api/diagrams/sitemap?get=1": { mermaid: "flowchart LR\n  r0(\"GET /x\")\n", ids: { r0: "route:GET /x" } },
  "api/routes": [
    { id: "route:GET /{lang}/notes", method: "GET", pattern: "/{lang}/notes", access: "authenticated", handler: "(*example.com/webapp/internal/web.noteHandler).list", page: true, pos: { file: "internal/web/router.go", startLine: 39 } },
    { id: "route:POST /{lang}/notes", method: "POST", pattern: "/{lang}/notes", access: "authenticated", handler: "(*example.com/webapp/internal/web.noteHandler).create", pos: {} },
    { id: "route:GET /{lang}/weather", method: "GET", pattern: "/{lang}/weather", access: "public", optionalAuth: true, handler: "example.com/webapp/internal/web.weatherPage$1", pos: {} },
  ],
  "api/diagrams/sitemap?get=1&access=authenticated": { mermaid: "flowchart LR\n  r0(\"GET /x\")\n", ids: { r0: "route:GET /x" } },
  [`api/page?${new URLSearchParams({ route: "GET /{lang}/notes/{id}" })}`]: {
    route: { id: "route:GET /{lang}/notes/{id}", method: "GET", pattern: "/{lang}/notes/{id}", access: "authenticated", handler: "(*example.com/webapp/internal/web.noteHandler).detail", evidence: "authenticated via (*example.com/webapp/internal/web.noteHandler).user", pos: { file: "internal/web/router.go", startLine: 41 } },
    renders: [{ edge: { id: 1, from: "", to: "", kind: "renders", pos: {}, attrs: { template: "note.html" } }, node: { id: "template:templates/note.html", kind: "template", name: "note.html", pos: { file: "templates/note.html", startLine: 1 } } }],
    requests: [
      { edge: { id: 2, from: "", to: "", kind: "requests", pos: {} }, node: { id: "htmx_call:a", kind: "htmx_call", name: "POST /{lang}/notes/{id}/share", pos: { file: "templates/note.html", startLine: 5 }, attrs: { method: "POST", url: "{{.ShareURL}}", trigger: "hx-post", template: "note.html", target: "POST /{lang}/notes/{id}/share" } } },
      { edge: { id: 3, from: "", to: "", kind: "requests", pos: {} }, node: { id: "htmx_call:b", kind: "htmx_call", name: "GET {?}", pos: {}, attrs: { method: "GET", url: "{{.X}}", trigger: "hx-get", template: "note.html", values: "{?}" } } },
    ],
    assets: [{ edge: { id: 4, from: "", to: "", kind: "loads", pos: { file: "templates/layout.html", startLine: 5 } }, node: { id: "static_asset:/static/app.css", kind: "static_asset", name: "/static/app.css", pos: {}, attrs: { file: "static/app.css" } } }],
    navigatesTo: [
      { edge: { id: 5, from: "", to: "", kind: "navigates_to", pos: { file: "templates/note.html", startLine: 2 }, attrs: { trigger: "link", template: "note.html" } }, node: { id: "route:GET /{lang}/notes", kind: "route", name: "GET /{lang}/notes", pos: {} } },
      { edge: { id: 6, from: "", to: "", kind: "navigates_to", pos: { file: "internal/web/handlers.go", startLine: 64 }, attrs: { trigger: "redirect", via: "(*example.com/webapp/internal/web.noteHandler).user" } }, node: { id: "route:GET /{lang}/sign-in", kind: "route", name: "GET /{lang}/sign-in", pos: {} } },
    ],
  },
  [`api/node?${new URLSearchParams({ id: "route:GET /{lang}/notes/{id}" })}`]: {
    node: { id: "route:GET /{lang}/notes/{id}", kind: "route", name: "GET /{lang}/notes/{id}", pos: {} },
    out: {},
    in: { navigates_to: [{ edge: { id: 7, from: "", to: "", kind: "navigates_to", pos: {}, attrs: { trigger: "link", template: "notes.html" } }, node: { id: "route:GET /{lang}/notes", kind: "route", name: "GET /{lang}/notes", pos: {} } }] },
  },
  [`api/diagrams/flow?${new URLSearchParams({ route: "POST /{lang}/notes", prune: "sinks" })}`]: {
    mermaid: "sequenceDiagram\n  actor Browser\n  Browser->>web: POST\n",
    ids: { "1": "method:(*example.com/webapp/internal/web.noteHandler).create", "2": "sink.sql:internal/store/postgres/notes.go:33:3" },
  },
  [`api/flow?${new URLSearchParams({ route: "POST /{lang}/notes", prune: "sinks" })}`]: {
    node: { id: "route:POST /{lang}/notes", kind: "route", name: "POST /{lang}/notes", pos: {} },
    edge: { id: 0, from: "", to: "", kind: "", pos: {} },
    children: [{
      node: { id: "method:h", kind: "method", name: "(*noteHandler).create", pos: { file: "internal/web/handlers.go", startLine: 88 } },
      edge: { id: 1, from: "", to: "", kind: "handled_by", pos: {} },
      children: [{
        node: { id: "sink.sql:x", kind: "sink.sql", name: "(*sql.Tx).ExecContext", detail: "INSERT INTO audit_events", pos: {} },
        edge: { id: 2, from: "", to: "", kind: "calls", pos: {} },
        children: [{ node: { id: "sql_table:audit_events", kind: "sql_table", name: "audit_events", pos: {} }, edge: { id: 3, from: "", to: "", kind: "queries", pos: {}, attrs: { op: "insert" } } }],
      }],
    }],
  },
  [`api/source?${new URLSearchParams({ file: "internal/web/handlers.go", start: "1" })}`]: {
    file: "internal/web/handlers.go", start: 1, end: 3, total: 3,
    lines: ["package web", "", "func (h *noteHandler) user() { h.auth.Authenticate(nil) }"],
  },
  [`api/refs?${new URLSearchParams({ file: "internal/web/handlers.go" })}`]: [
    { line: 3, col: 39, endCol: 51, name: "Authenticate", kind: "method", target: { file: "internal/web/auth.go", startLine: 32 }, node: "method:(*example.com/webapp/internal/web.Authenticator).Authenticate" },
  ],
  "api/table?name=notes": {
    table: { id: "sql_table:notes", kind: "sql_table", name: "notes", pos: { file: "migrations/0002_notes.up.sql", startLine: 1 } },
    columns: [
      { id: "sql_column:notes.id", kind: "sql_column", name: "id", detail: "bigserial", pos: {}, attrs: { primaryKey: "true", notNull: "true" } },
      { id: "sql_column:notes.owner_id", kind: "sql_column", name: "owner_id", detail: "int8", pos: {}, attrs: { notNull: "true" } },
    ],
    references: [{ edge: { id: 1, from: "sql_table:notes", to: "sql_table:users", kind: "fk", pos: {}, attrs: { columns: "owner_id", references: "users.id", onDelete: "cascade" } }, node: { id: "sql_table:users", kind: "sql_table", name: "users", pos: {} } }],
    referencedBy: [{ edge: { id: 2, from: "sql_table:audit_events", to: "sql_table:notes", kind: "fk", pos: {}, attrs: { columns: "note_id", onDelete: "set null" } }, node: { id: "sql_table:audit_events", kind: "sql_table", name: "audit_events", pos: {} } }],
    queries: [{ edge: { id: 3, from: "", to: "", kind: "queries", pos: {}, attrs: { op: "insert" } }, node: { id: "sink.sql:x", kind: "sink.sql", name: "(*sql.Tx).QueryRowContext", detail: "INSERT INTO notes ...", pos: { file: "internal/store/postgres/notes.go", startLine: 33 }, attrs: { caller: "(*example.com/webapp/internal/store/postgres.NoteRepository).Create" } } }],
    routes: [{ route: "POST /{lang}/notes", op: "insert" }, { route: "GET /{lang}/notes", op: "select" }],
  },
  "api/diagrams/er?table=notes&depth=1": { mermaid: "erDiagram\n  notes {\n    int8 id PK\n  }\n", ids: { t0: "sql_table:notes" } },
  "api/node?id=func%3Aexample.com%2Fapp.F": {
    node: { id: "func:example.com/app.F", kind: "func", name: "F", package: "example.com/app", pos: { file: "app.go", startLine: 3 } },
    out: { calls: [{ edge: { id: 1, from: "func:example.com/app.F", to: "func:example.com/app.G", kind: "calls", pos: {} }, node: { id: "func:example.com/app.G", kind: "func", name: "G", pos: {} } }] },
    in: {},
  },
};

function mockFetch() {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    const body = responses[url];
    return { ok: body !== undefined, status: body ? 200 : 404, statusText: "", json: async () => body ?? { error: "not found" } };
  }));
}

async function render(hash: string) {
  window.location.hash = hash;
  const el = document.createElement("div");
  document.body.append(el);
  await act(async () => createRoot(el).render(<App />));
  await act(async () => new Promise((r) => setTimeout(r, 0)));
  return el;
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("App", () => {
  it("shows the overview with counts and the site map preview", async () => {
    mockFetch();
    const el = await render("#/home");
    expect(el.querySelector("h1")?.textContent).toBe("example.com/webapp");
    expect(el.textContent).toContain("13 routes");
    expect(el.querySelector(".diagram-canvas svg")).not.toBeNull();
    expect(el.querySelector(".icb-clickable")).not.toBeNull();
  });

  it("shows a node with its edges", async () => {
    mockFetch();
    const el = await render("#/node?id=" + encodeURIComponent("func:example.com/app.F"));
    expect(el.querySelector("h1")?.textContent).toContain("F");
    expect(el.textContent).toContain("calls →");
    expect(el.querySelector("a.pos")?.textContent).toBe("app.go:3");
  });

  it("lists routes in the site map table, filtered", async () => {
    mockFetch();
    const el = await render("#/sitemap?mode=table");
    const rows = [...el.querySelectorAll("table.routes tbody tr")].map((tr) => tr.textContent);
    expect(rows).toHaveLength(2); // GET only by default
    expect(rows[0]).toContain("/{lang}/notes");
    expect(rows[0]).toContain("(*web.noteHandler).list");
    expect(rows[1]).toContain("public · session-aware");
    const filtered = await render("#/sitemap?mode=table&access=authenticated");
    expect(filtered.querySelectorAll("table.routes tbody tr")).toHaveLength(1);
  });

  it("draws the filtered site map diagram", async () => {
    mockFetch();
    const el = await render("#/sitemap?access=authenticated");
    expect(el.querySelector(".diagram-canvas svg")).not.toBeNull();
    expect(el.querySelector(".icb-clickable")).not.toBeNull();
  });

  it("drills into a page: requests, assets and navigation with sources", async () => {
    mockFetch();
    const el = await render("#/route?" + new URLSearchParams({ id: "GET /{lang}/notes/{id}" }));
    const text = el.textContent ?? "";
    expect(el.querySelector("h1")?.textContent).toContain("/{lang}/notes/{id}");
    expect(text).toContain("authenticated via (*web.noteHandler).user");
    expect(text).toContain("POST /{lang}/notes/{id}/share");
    expect(text).toContain("unresolved: {?}");
    expect(text).toContain("/static/app.css");
    expect(text).toContain("by (*web.noteHandler).user");
    expect(text).toContain("Reached from (1)");
    expect([...el.querySelectorAll("a.pos")].map((a) => a.textContent)).toContain("templates/note.html:5");
  });

  it("shows a flow as numbered sequence steps and as a call tree", async () => {
    mockFetch();
    const seq = await render("#/flow?" + new URLSearchParams({ route: "POST /{lang}/notes" }));
    const steps = [...seq.querySelectorAll("ol.steps li")].map((li) => li.textContent);
    expect(steps).toEqual(["(*web.noteHandler).create", "sink.sql at internal/store/postgres/notes.go:33:3"]);
    expect(seq.querySelector(".diagram-canvas svg")).not.toBeNull();
    const tree = await render("#/flow?" + new URLSearchParams({ route: "POST /{lang}/notes", mode: "tree" }));
    expect(tree.textContent).toContain("insert table");
    expect(tree.textContent).toContain("INSERT INTO audit_events");
  });

  it("shows highlighted code with identifiers linked to definitions", async () => {
    mockFetch();
    const el = await render("#/code?" + new URLSearchParams({ file: "internal/web/handlers.go", line: "3" }));
    await act(async () => new Promise((r) => setTimeout(r, 300))); // Shiki loads lazily
    const lines = el.querySelectorAll(".code .line");
    expect(lines).toHaveLength(3);
    expect(lines[2].classList.contains("marked")).toBe(true);
    const ref = el.querySelector<HTMLAnchorElement>(".code a.ref");
    expect(ref?.textContent).toBe("Authenticate");
    expect(decodeURIComponent(ref!.getAttribute("href")!)).toBe("#/code?file=internal/web/auth.go&line=32");
    expect(lines[2].textContent).toBe("3func (h *noteHandler) user() { h.auth.Authenticate(nil) }");
  });

  it("shows a table: columns, relations, routes and queries", async () => {
    mockFetch();
    const el = await render("#/table?name=notes");
    const text = el.textContent ?? "";
    expect(el.querySelector("h1")?.textContent).toContain("notes");
    expect(text).toContain("users.id on delete cascade");
    expect(text).toContain("Referenced by (1)");
    expect(text).toContain("audit_events.note_id on delete set null");
    expect(text).toContain("POST /{lang}/notes insert");
    expect(text).toContain("(*postgres.NoteRepository).Create");
    expect(el.querySelector(".diagram-canvas svg")).not.toBeNull();
  });

  it("toggles the theme", async () => {
    mockFetch();
    const el = await render("#/home");
    const before = document.documentElement.dataset.theme;
    await act(async () => el.querySelector<HTMLButtonElement>("button.theme")!.click());
    expect(document.documentElement.dataset.theme).not.toBe(before);
  });
});
