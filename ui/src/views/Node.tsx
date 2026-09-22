import { api, type Neighbor } from "../api";
import { KindBadge, Loaded, PosLink } from "../components/Common";
import { nodeHref } from "../router";
import { useAsync } from "../useAsync";

// NodeView shows any graph node: what it is, where, and its edges.
export function NodeView({ id }: { id: string }) {
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
          <Edges title="Outgoing" groups={d.out} />
          <Edges title="Incoming" groups={d.in} incoming />
        </section>
      )}
    </Loaded>
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
