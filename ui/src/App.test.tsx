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

  it("toggles the theme", async () => {
    mockFetch();
    const el = await render("#/home");
    const before = document.documentElement.dataset.theme;
    await act(async () => el.querySelector<HTMLButtonElement>("button.theme")!.click());
    expect(document.documentElement.dataset.theme).not.toBe(before);
  });
});
