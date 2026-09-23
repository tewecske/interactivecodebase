import { api, type Neighbor } from "../api";
import { AccessBadge, Loaded, PosLink } from "../components/Common";
import { href, nodeHref } from "../router";
import { useAsync } from "../useAsync";
import { shortFunc } from "./SiteMap";

// RouteView is a route's page drill-down: what it is, who may call it,
// what its page requests and loads, and where you can go from it.
export function RouteView({ routeKey }: { routeKey: string }) {
  const page = useAsync(() => api.page(routeKey), [routeKey]);
  const node = useAsync(() => api.node(`route:${routeKey}`), [routeKey]);
  return (
    <Loaded state={page}>
      {(p) => {
        const r = p.route;
        const handlerID = r.handler.startsWith("(") ? `method:${r.handler}` : `func:${r.handler}`;
        return (
          <section>
            <h1>
              <span className="method">{r.method}</span> {r.pattern} <AccessBadge access={r.access} optional={r.optionalAuth} />
            </h1>
            <p className="muted">
              registered at <PosLink pos={r.pos} />
              {r.conditional && " · depends on runtime configuration"}
              {r.static && " · serves static files"}
            </p>
            <div className="actions">
              <a className="button" href={href("flow", { route: routeKey })}>
                Call flow ↓ SQL
              </a>
              <a className="button" href={href("node", { id: handlerID })}>
                Handler code
              </a>
            </div>
            <table className="grid">
              <tbody>
                <tr>
                  <th>Handler</th>
                  <td className="mono">
                    <a href={href("node", { id: handlerID })}>{shortFunc(r.handler)}</a>
                  </td>
                </tr>
                {r.middleware && (
                  <tr>
                    <th>Middleware</th>
                    <td className="mono">{r.middleware.map(shortFunc).join(" › ")}</td>
                  </tr>
                )}
                <tr>
                  <th>Access</th>
                  <td>
                    <AccessBadge access={r.access} optional={r.optionalAuth} /> <span className="muted mono">{r.evidence ? shortFunc(r.evidence) : "no guard"}</span>
                  </td>
                </tr>
                {r.variants && (
                  <tr>
                    <th>Variants</th>
                    <td className="mono">{r.variants.join("  ")}</td>
                  </tr>
                )}
                {p.renders.length > 0 && (
                  <tr>
                    <th>Templates</th>
                    <td>
                      {p.renders.map((t) => (
                        <span key={t.node.id}>
                          <a href={href("code", { file: t.node.pos.file!, line: "1" })}>{t.node.name}</a>{" "}
                          <span className="muted">({t.edge.attrs?.template})</span>{" "}
                        </span>
                      ))}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>

            <Requests requests={p.requests} />
            <Assets assets={p.assets} />
            <Navigation title="Navigates to" edges={p.navigatesTo} />
            <Loaded state={node}>{(n) => <Navigation title="Reached from" edges={n.in.navigates_to ?? []} />}</Loaded>
          </section>
        );
      }}
    </Loaded>
  );
}

function Requests({ requests }: { requests: Neighbor[] }) {
  if (requests.length === 0) return null;
  return (
    <>
      <h2>Requests this page makes ({requests.length})</h2>
      <table className="grid">
        <thead>
          <tr>
            <th>Target</th>
            <th>Trigger</th>
            <th>In template</th>
          </tr>
        </thead>
        <tbody>
          {requests.map(({ node }) => {
            const a = node.attrs ?? {};
            const targets = a.target ? a.target.split(",") : [];
            return (
              <tr key={node.id}>
                <td>
                  {targets.length > 0 ? (
                    targets.map((t) => (
                      <div key={t}>
                        <a href={href("route", { id: t })}>{t}</a>
                      </div>
                    ))
                  ) : (
                    <span className="muted" title="URL could not be resolved to a route">
                      {a.method} {a.url} (unresolved: {a.values?.split("\n").join(" | ")})
                    </span>
                  )}
                </td>
                <td>
                  <code>{a.trigger}</code>
                </td>
                <td>
                  {a.template} <PosLink pos={node.pos} />
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </>
  );
}

function Assets({ assets }: { assets: Neighbor[] }) {
  if (assets.length === 0) return null;
  return (
    <>
      <h2>Static resources ({assets.length})</h2>
      <ul className="edges">
        {assets.map(({ node, edge }) => (
          <li key={node.id}>
            <code>{node.name}</code>{" "}
            {node.attrs?.file ? <a href={href("code", { file: node.attrs.file, line: "1" })}>{node.attrs.file}</a> : <span className="muted">not found on disk</span>}{" "}
            <span className="muted">from</span> <PosLink pos={edge.pos} />
          </li>
        ))}
      </ul>
    </>
  );
}

const triggerLabels: Record<string, string> = { link: "Links", form: "Form submissions", redirect: "Redirects", "hx-redirect": "HTMX redirects" };

function Navigation({ title, edges }: { title: string; edges: Neighbor[] }) {
  if (edges.length === 0) return null;
  const byTrigger: Record<string, Neighbor[]> = {};
  for (const nb of edges) (byTrigger[nb.edge.attrs?.trigger ?? "link"] ??= []).push(nb);
  return (
    <>
      <h2>
        {title} ({edges.length})
      </h2>
      {Object.entries(byTrigger).map(([trigger, list]) => (
        <details key={trigger} open>
          <summary>{triggerLabels[trigger] ?? trigger}</summary>
          <ul className="edges">
            {list.map(({ node, edge }) => (
              <li key={edge.id}>
                <a href={nodeHref(node.id)}>{node.name}</a>{" "}
                <span className="muted">
                  {edge.attrs?.template ? `in ${edge.attrs.template}` : edge.attrs?.via ? `by ${shortFunc(edge.attrs.via)}` : ""}
                </span>{" "}
                <PosLink pos={edge.pos} />
              </li>
            ))}
          </ul>
        </details>
      ))}
    </>
  );
}
