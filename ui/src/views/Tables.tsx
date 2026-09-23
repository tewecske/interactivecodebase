import { api, type Neighbor } from "../api";
import { Loaded, PosLink } from "../components/Common";
import { MermaidView } from "../components/Mermaid";
import { href, navigate } from "../router";
import type { Theme } from "../theme";
import { useAsync } from "../useAsync";
import { shortFunc } from "./SiteMap";

const toTable = (id: string) => navigate("table", { name: id.slice("sql_table:".length) });

// TablesView lists the schema's tables and draws the whole schema.
export function TablesView({ theme }: { theme: Theme }) {
  const tables = useAsync(() => api.tables(), []);
  const er = useAsync(() => api.diagram("er", {}), []);
  return (
    <section>
      <h1>Tables</h1>
      <Loaded state={tables}>
        {(ts) => (
          <table className="grid">
            <thead>
              <tr>
                <th>Table</th>
                <th>Columns</th>
                <th>Defined in</th>
              </tr>
            </thead>
            <tbody>
              {[...ts]
                .sort((a, b) => a.name.localeCompare(b.name))
                .map((t) => (
                  <tr key={t.id}>
                    <td>
                      <a href={href("table", { name: t.name })}>{t.name}</a>
                      {t.attrs?.inferred === "true" && <span className="badge kind" title="not created by the migrations; seen in queries">inferred</span>}
                    </td>
                    <td className="mono muted">{t.detail}</td>
                    <td>
                      <PosLink pos={t.pos} />
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        )}
      </Loaded>
      <h2>Schema</h2>
      <Loaded state={er}>{(d) => <MermaidView diagram={d} theme={theme} onNodeClick={toTable} />}</Loaded>
    </section>
  );
}

// TableView shows one table: columns, relations, and who reads or writes it.
export function TableView({ params, theme }: { params: Record<string, string>; theme: Theme }) {
  const name = params.name;
  const depth = params.depth ?? "1";
  const detail = useAsync(() => api.table(name), [name]);
  const er = useAsync(() => api.diagram("er", { table: name, depth }), [name, depth]);
  return (
    <Loaded state={detail}>
      {(d) => {
        const fkByColumn = new Map<string, Neighbor>();
        for (const fk of d.references) for (const c of (fk.edge.attrs?.columns ?? "").split(",")) fkByColumn.set(c, fk);
        return (
          <section>
            <h1>
              <span className="badge kind">table</span> {d.table.name}
            </h1>
            <p className="muted">
              {d.table.attrs?.inferred === "true" ? "not created by the migrations; seen in queries" : <>defined at <PosLink pos={d.table.pos} /></>}
            </p>
            {d.columns.length > 0 && (
              <table className="grid">
                <thead>
                  <tr>
                    <th>Column</th>
                    <th>Type</th>
                    <th>Constraints</th>
                    <th>References</th>
                  </tr>
                </thead>
                <tbody>
                  {d.columns.map((c) => {
                    const fk = fkByColumn.get(c.name);
                    return (
                      <tr key={c.id}>
                        <td className="mono">{c.name}</td>
                        <td className="mono muted">{c.detail}</td>
                        <td>
                          {c.attrs?.primaryKey === "true" && <span className="badge kind">PK</span>} {c.attrs?.notNull === "true" && <span className="badge kind">NOT NULL</span>}
                        </td>
                        <td>
                          {fk && (
                            <>
                              <a href={href("table", { name: fk.node.name })}>{fk.edge.attrs?.references}</a>
                              {fk.edge.attrs?.onDelete && <span className="muted"> on delete {fk.edge.attrs.onDelete}</span>}
                            </>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
            {d.referencedBy.length > 0 && (
              <>
                <h2>Referenced by ({d.referencedBy.length})</h2>
                <ul className="edges">
                  {d.referencedBy.map((fk) => (
                    <li key={fk.edge.id}>
                      <a href={href("table", { name: fk.node.name })}>{fk.node.name}</a>.<span className="mono">{fk.edge.attrs?.columns}</span>
                      {fk.edge.attrs?.onDelete && <span className="muted"> on delete {fk.edge.attrs.onDelete}</span>} <PosLink pos={fk.edge.pos} />
                    </li>
                  ))}
                </ul>
              </>
            )}
            <h2>
              Relations{" "}
              <select value={depth} onChange={(e) => navigate("table", { name, depth: e.target.value })}>
                <option value="1">direct</option>
                <option value="2">2 hops</option>
                <option value="3">3 hops</option>
              </select>
            </h2>
            <Loaded state={er}>{(g) => <MermaidView diagram={g} theme={theme} onNodeClick={toTable} />}</Loaded>
            <h2>Routes that touch it ({d.routes.length})</h2>
            <ul className="edges">
              {groupRoutes(d.routes).map(([route, ops]) => (
                <li key={route}>
                  <a href={href("route", { id: route })}>{route}</a> <span className="muted">{ops.join(", ")}</span>{" "}
                  <a className="muted" href={href("flow", { route })}>
                    flow
                  </a>
                </li>
              ))}
            </ul>
            <h2>Queries ({d.queries.length})</h2>
            <table className="grid">
              <tbody>
                {d.queries.map((q) => (
                  <tr key={q.edge.id}>
                    <td>
                      <span className="badge kind">{q.edge.attrs?.op}</span>
                    </td>
                    <td>
                      <a href={href("node", { id: q.node.id })} className="mono">
                        {shortFunc(q.node.attrs?.caller ?? q.node.name)}
                      </a>{" "}
                      <PosLink pos={q.node.pos} />
                      <pre className="detail small">{q.node.detail}</pre>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        );
      }}
    </Loaded>
  );
}

function groupRoutes(routes: { route: string; op: string }[]): [string, string[]][] {
  const m = new Map<string, string[]>();
  for (const r of routes) m.set(r.route, [...(m.get(r.route) ?? []), r.op]);
  return [...m.entries()];
}
