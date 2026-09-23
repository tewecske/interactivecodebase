import { api, type GraphNode, type Neighbor } from "../api";
import { Code } from "../components/Code";
import { KindBadge, Loaded, PosLink } from "../components/Common";
import { MermaidView } from "../components/Mermaid";
import { navigate, nodeHref } from "../router";
import type { Theme } from "../theme";
import { useAsync } from "../useAsync";

const withTypes = ["func", "method", "type"];

// NodeView shows any graph node: what it is, where, its source, the types
// it involves, and its edges.
export function NodeView({ id, theme }: { id: string; theme: Theme }) {
  const detail = useAsync(() => api.node(id), [id]);
  return (
    <Loaded state={detail}>
      {(d) => (
        <section>
          <h1>
            <KindBadge kind={d.node.kind} /> {d.node.name}
          </h1>
          <p className="muted">
            {d.node.package} <PosLink pos={d.node.pos} />
          </p>
          {d.node.detail && <pre className="detail">{d.node.detail}</pre>}
          {d.node.attrs && (
            <table className="grid">
              <tbody>
                {Object.entries(d.node.attrs).map(([k, v]) => (
                  <tr key={k}>
                    <th>{k}</th>
                    <td>{v}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {d.node.kind === "sink.sql" && <SQL out={d.out} />}
          <Source node={d.node} theme={theme} />
          {withTypes.includes(d.node.kind) && <Types id={d.node.id} theme={theme} />}
          <Edges title="Outgoing" groups={d.out} />
          <Edges title="Incoming" groups={d.in} incoming />
        </section>
      )}
    </Loaded>
  );
}

// SQL summarizes what an SQL sink touches.
function SQL({ out }: { out: Record<string, Neighbor[]> }) {
  const tables = out.queries ?? [];
  if (tables.length === 0) return null;
  return (
    <>
      <h2>Tables</h2>
      <table className="grid">
        <tbody>
          {tables.map((t) => (
            <tr key={t.edge.id}>
              <td>
                <a href={nodeHref(t.node.id)}>{t.node.name}</a>
              </td>
              <td>
                <span className="badge kind">{t.edge.attrs?.op}</span>
              </td>
              <td className="mono muted">{t.edge.attrs?.columns?.split(",").join(", ")}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

// Source shows the node's code: its whole range, or context around its
// position for nodes that only mark a line (types, sinks, calls).
function Source({ node, theme }: { node: GraphNode; theme: Theme }) {
  const p = node.pos;
  if (!p?.file || !p.startLine || node.attrs?.external === "true") return null;
  const from = p.endLine ? p.startLine : Math.max(1, p.startLine - 3);
  const to = p.endLine ?? p.startLine + 12;
  return (
    <>
      <h2>Source</h2>
      <Code file={p.file} from={from} to={Math.min(to, from + 400)} markFrom={p.startLine} theme={theme} />
    </>
  );
}

// Types draws the types a function or type involves.
function Types({ id, theme }: { id: string; theme: Theme }) {
  const diagram = useAsync(() => api.diagram("types", { id }), [id]);
  if (!diagram.loading && "data" in diagram && Object.keys(diagram.data.ids).length === 0) return null;
  return (
    <>
      <h2>Types involved</h2>
      <Loaded state={diagram}>{(d) => <MermaidView diagram={d} theme={theme} onNodeClick={(t) => navigate("node", { id: t })} />}</Loaded>
    </>
  );
}

export function Edges({ title, groups, incoming }: { title: string; groups: Record<string, Neighbor[]>; incoming?: boolean }) {
  const kinds = Object.keys(groups).sort();
  if (kinds.length === 0) return null;
  return (
    <>
      <h2>{title}</h2>
      {kinds.map((kind) => (
        <details key={kind} open={groups[kind].length <= 30}>
          <summary>
            {incoming ? `← ${kind}` : `${kind} →`} ({groups[kind].length})
          </summary>
          <ul className="edges">
            {groups[kind].map((nb) => (
              <li key={`${nb.edge.id}`}>
                <a href={nodeHref(nb.node.id)}>{nb.node.name}</a> <KindBadge kind={nb.node.kind} /> <PosLink pos={nb.edge.pos} />
              </li>
            ))}
          </ul>
        </details>
      ))}
    </>
  );
}
