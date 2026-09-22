import { api } from "../api";
import { Loaded } from "../components/Common";
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
