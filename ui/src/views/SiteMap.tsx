import { useMemo } from "react";
import { api, type RouteInfo } from "../api";
import { AccessBadge, Loaded, PosLink } from "../components/Common";
import { MermaidView } from "../components/Mermaid";
import { href, navigate } from "../router";
import type { Theme } from "../theme";
import { useAsync } from "../useAsync";

export const accessGroups = [
  { key: "public", label: "Public" },
  { key: "optional", label: "Session-aware" },
  { key: "guest", label: "Guest" },
  { key: "authenticated", label: "Signed in" },
  { key: "admin", label: "Admin" },
];

export function groupOf(r: RouteInfo): string {
  return r.access === "public" && r.optionalAuth ? "optional" : r.access;
}

// SiteMap shows every route grouped by access level, as a diagram with
// navigation edges or as a filterable table.
export function SiteMap({ params, theme }: { params: Record<string, string>; theme: Theme }) {
  const mode = params.mode === "table" ? "table" : "diagram";
  const getOnly = params.all !== "1";
  const text = params.q ?? "";
  const access = useMemo(() => (params.access ? params.access.split(",") : []), [params.access]);
  const set = (p: Record<string, string | undefined>) =>
    navigate("sitemap", { mode, all: getOnly ? undefined : "1", q: text || undefined, access: access.join(",") || undefined, ...p });

  return (
    <section>
      <h1>Site map</h1>
      <div className="toolbar">
        <div className="segmented">
          <button className={mode === "diagram" ? "on" : ""} onClick={() => set({ mode: "diagram" })}>Diagram</button>
          <button className={mode === "table" ? "on" : ""} onClick={() => set({ mode: "table" })}>Table</button>
        </div>
        <label>
          <input type="checkbox" checked={!getOnly} onChange={(e) => set({ all: e.target.checked ? "1" : undefined })} /> non-GET routes
        </label>
        {accessGroups.map((g) => (
          <label key={g.key} className={`access-${g.key}`}>
            <input
              type="checkbox"
              checked={access.length === 0 || access.includes(g.key)}
              onChange={(e) => {
                const current = access.length === 0 ? accessGroups.map((a) => a.key) : access;
                const next = e.target.checked ? [...current, g.key] : current.filter((k) => k !== g.key);
                set({ access: next.length === accessGroups.length ? undefined : next.join(",") || "none" });
              }}
            />{" "}
            {g.label}
          </label>
        ))}
        <input
          className="filter"
          type="search"
          placeholder="filter by path…"
          defaultValue={text}
          onKeyDown={(e) => e.key === "Enter" && set({ q: (e.target as HTMLInputElement).value || undefined })}
        />
      </div>
      {mode === "diagram" ? (
        <Diagram getOnly={getOnly} access={access} text={text} theme={theme} />
      ) : (
        <RouteTable getOnly={getOnly} access={access} text={text} />
      )}
      <p className="muted legend">
        Arrows: → link · ⇒ form submission · ⇢ redirect. Click a route to open its page. Drag to pan, Ctrl/⌘ + wheel to zoom.
      </p>
    </section>
  );
}

function Diagram({ getOnly, access, text, theme }: { getOnly: boolean; access: string[]; text: string; theme: Theme }) {
  const diagram = useAsync(
    () => api.diagram("sitemap", { get: getOnly ? "1" : undefined, access: access.join(",") || undefined, q: text || undefined }),
    [getOnly, access.join(","), text],
  );
  return (
    <Loaded state={diagram}>
      {(d) =>
        Object.keys(d.ids).length === 0 ? (
          <p className="muted">No routes match.</p>
        ) : (
          <MermaidView diagram={d} theme={theme} onNodeClick={(id) => navigate("route", { id: id.slice("route:".length) })} />
        )
      }
    </Loaded>
  );
}

function RouteTable({ getOnly, access, text }: { getOnly: boolean; access: string[]; text: string }) {
  const routes = useAsync(() => api.routes(), []);
  return (
    <Loaded state={routes}>
      {(all) => {
        const rows = all.filter(
          (r) =>
            (!getOnly || r.method === "GET") &&
            (access.length === 0 || access.includes(groupOf(r))) &&
            (!text || r.pattern.toLowerCase().includes(text.toLowerCase())),
        );
        return (
          <table className="grid routes">
            <thead>
              <tr>
                <th>Method</th>
                <th>Pattern</th>
                <th>Access</th>
                <th>Handler</th>
                <th>Registered</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.id}>
                  <td>{r.method}</td>
                  <td>
                    <a href={href("route", { id: `${r.method} ${r.pattern}` })}>{r.pattern}</a>
                    {r.page && <span className="badge kind">page</span>}
                    {r.conditional && <span className="badge kind" title="depends on runtime configuration">conditional</span>}
                  </td>
                  <td>
                    <AccessBadge access={r.access} optional={r.optionalAuth} />
                  </td>
                  <td className="mono">
                    {r.middleware?.map((m) => shortFunc(m) + " › ")}
                    {shortFunc(r.handler)}
                  </td>
                  <td>
                    <PosLink pos={r.pos} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        );
      }}
    </Loaded>
  );
}

// shortFunc trims the package path from a go/ssa function name.
export function shortFunc(name: string): string {
  return name.replace(/[\w.-]+(\/[\w.-]+)+\./g, (m) => m.slice(m.lastIndexOf("/") + 1));
}
