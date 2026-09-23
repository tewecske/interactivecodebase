// Typed access to the icb JSON API.

export interface Pos {
  file?: string;
  startLine?: number;
  startCol?: number;
  endLine?: number;
  endCol?: number;
}

export interface GraphNode {
  id: string;
  kind: string;
  name: string;
  package?: string;
  detail?: string;
  pos: Pos;
  attrs?: Record<string, string>;
}

export interface GraphEdge {
  id: number;
  from: string;
  to: string;
  kind: string;
  pos: Pos;
  attrs?: Record<string, string>;
}

export interface Neighbor {
  edge: GraphEdge;
  node: GraphNode;
}

export interface Summary {
  module: string;
  dir: string;
  packages: number;
  moduleFunctions: number;
  counts: { nodes: Record<string, number>; edges: Record<string, number> };
  migrationDirs: string[];
  analysisMs: number;
}

export interface RouteInfo {
  id: string;
  method: string;
  pattern: string;
  variants?: string[];
  access: string;
  optionalAuth?: boolean;
  handler: string;
  middleware?: string[];
  page?: boolean;
  static?: boolean;
  conditional?: boolean;
  evidence?: string;
  pos: Pos;
}

export interface NodeDetail {
  node: GraphNode;
  out: Record<string, Neighbor[]>;
  in: Record<string, Neighbor[]>;
}

export interface PageDetail {
  route: RouteInfo;
  renders: Neighbor[];
  requests: Neighbor[];
  assets: Neighbor[];
  navigatesTo: Neighbor[];
}

export interface FlowStep {
  node: GraphNode;
  edge: GraphEdge;
  children?: FlowStep[];
  cycle?: boolean;
  ref?: boolean;
  truncated?: boolean;
  calls?: number;
}

export interface Source {
  file: string;
  start: number;
  end: number;
  total: number;
  lines: string[];
}

export interface Ref {
  line: number;
  col: number;
  endCol: number;
  name: string;
  target?: Pos;
  node?: string;
  kind: string;
}

export interface TableDetail {
  table: GraphNode;
  columns: GraphNode[];
  references: Neighbor[];
  referencedBy: Neighbor[];
  queries: Neighbor[];
  routes: { route: string; op: string }[];
}

export interface Diagram {
  mermaid: string;
  ids: Record<string, string>;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

async function get<T>(path: string, params: Record<string, string | undefined> = {}): Promise<T> {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) q.set(k, v);
  const res = await fetch(`api/${path}${q.size ? `?${q}` : ""}`);
  const body = await res.json();
  if (!res.ok) throw new ApiError(res.status, body.error ?? res.statusText);
  return body as T;
}

export const api = {
  summary: () => get<Summary>("summary"),
  routes: (filter: { access?: string; method?: string; q?: string } = {}) => get<RouteInfo[]>("routes", filter),
  node: (id: string) => get<NodeDetail>("node", { id }),
  page: (route: string) => get<PageDetail>("page", { route }),
  flow: (route: string, method?: string, prune?: string) => get<FlowStep>("flow", { route, method, prune }),
  source: (file: string, start?: number, end?: number) =>
    get<Source>("source", { file, start: start?.toString(), end: end?.toString() }),
  refs: (file: string) => get<Ref[]>("refs", { file }),
  tables: () => get<GraphNode[]>("tables"),
  table: (name: string) => get<TableDetail>("table", { name }),
  search: (q: string, kind?: string) => get<GraphNode[]>("search", { q, kind }),
  diagram: (kind: "sitemap" | "flow" | "types" | "er", params: Record<string, string | undefined>) =>
    get<Diagram>(`diagrams/${kind}`, params),
};
