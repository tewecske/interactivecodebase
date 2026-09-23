import { api } from "../api";
import { Loaded, PosLink } from "../components/Common";
import { MermaidView } from "../components/Mermaid";
import { href, navigate, nodeHref } from "../router";
import type { Theme } from "../theme";
import { useAsync } from "../useAsync";

// hashParts splits "#/view?a=b" into navigate's arguments.
function hashParts(hash: string): [string, Record<string, string>] {
  const [path, query = ""] = hash.replace(/^#\//, "").split("?", 2);
  return [path, Object.fromEntries(new URLSearchParams(query))];
}

export function Home({ theme }: { theme: Theme }) {
  const summary = useAsync(() => api.summary(), []);
  const pages = useAsync(() => api.diagram("sitemap", { get: "1" }), []);
  const entries = useAsync(() => api.entries(), []);
  return (
    <Loaded state={summary}>
      {(s) => (
        <section>
          <h1>{s.module}</h1>
          <p className="muted">
            {s.dir} · {s.packages} packages · {s.moduleFunctions} functions · analyzed in {(s.analysisMs / 1000).toFixed(1)}s
          </p>
          <div className="cards">
            <a className="card" href={href("sitemap")}>
              <strong>{s.counts.nodes.route ?? 0}</strong> routes
            </a>
            {(s.counts.nodes.entry ?? 0) > 0 && (
              <a className="card" href={href("search", { kind: "entry" })}>
                <strong>{s.counts.nodes.entry}</strong> other entry points
              </a>
            )}
            <a className="card" href={href("tables")}>
              <strong>{s.counts.nodes.sql_table ?? 0}</strong> tables
            </a>
            <a className="card" href={href("search", { kind: "sink.sql" })}>
              <strong>{s.counts.nodes["sink.sql"] ?? 0}</strong> SQL queries
            </a>
            <div className="card">
              <strong>{s.counts.nodes.template ?? 0}</strong> templates
            </div>
            <div className="card">
              <strong>{s.counts.edges.navigates_to ?? 0}</strong> navigation links
            </div>
            <div className="card">
              <strong>{(s.counts.nodes.func ?? 0) + (s.counts.nodes.method ?? 0)}</strong> functions in the graph
            </div>
          </div>
          <h2>
            Pages and navigation <a href={href("sitemap")}>open site map</a>
          </h2>
          <Loaded state={pages}>
            {(d) => <MermaidView diagram={d} theme={theme} onNodeClick={(id) => navigate(...hashParts(nodeHref(id)))} />}
          </Loaded>
          <Loaded state={entries}>
            {(list) =>
              list.length > 0 && (
                <>
                  <h2>Other entry points</h2>
                  <p className="muted">Workers started from main and the jobs they run, commands, gRPC methods and message consumers.</p>
                  <table className="grid entries">
                    <tbody>
                      {list.map((e) => (
                        <tr key={e.id}>
                          <td>
                            <span className={`badge entry-${e.attrs?.entryKind}`}>{e.attrs?.entryKind}</span>
                          </td>
                          <td>
                            {e.attrs?.parent && e.attrs.entryKind === "job" && <span className="muted">{e.attrs.parent} › </span>}
                            <a href={nodeHref(e.id)} title="Show its flow">
                              {e.name}
                            </a>
                            {e.detail && <span className="muted"> ({e.detail})</span>}
                          </td>
                          <td>
                            <code>{e.attrs?.handler}</code>
                          </td>
                          <td>
                            <PosLink pos={e.pos} />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </>
              )
            }
          </Loaded>
          <h2>Graph</h2>
          <table className="grid">
            <tbody>
              {Object.entries(s.counts.nodes)
                .sort((a, b) => b[1] - a[1])
                .map(([k, n]) => (
                  <tr key={k}>
                    <td>{k}</td>
                    <td className="num">{n}</td>
                  </tr>
                ))}
            </tbody>
          </table>
        </section>
      )}
    </Loaded>
  );
}
