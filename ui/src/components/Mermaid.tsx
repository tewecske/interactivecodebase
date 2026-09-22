// Renders Mermaid source, makes nodes that map to graph IDs clickable, and
// supports pan (drag) and zoom (wheel).
import mermaid from "mermaid";
import { useEffect, useId, useRef, useState } from "react";
import type { Diagram } from "../api";
import type { Theme } from "../theme";

let renderSeq = 0;

export function MermaidView({
  diagram,
  theme,
  onNodeClick,
}: {
  diagram: Diagram;
  theme: Theme;
  onNodeClick?: (graphId: string) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);
  const [view, setView] = useState({ x: 0, y: 0, scale: 1 });
  const drag = useRef<{ x: number; y: number } | null>(null);
  const baseId = useId().replace(/[^a-zA-Z0-9]/g, "");

  useEffect(() => {
    let live = true;
    mermaid.initialize({
      startOnLoad: false,
      theme: theme === "dark" ? "dark" : "default",
      securityLevel: "strict",
      maxTextSize: 500_000,
      maxEdges: 5_000,
      flowchart: { htmlLabels: false },
    });
    const id = `m${baseId}${++renderSeq}`;
    mermaid
      .render(id, diagram.mermaid)
      .then(({ svg }) => {
        if (!live || !host.current) return;
        host.current.innerHTML = svg;
        setError(null);
        bindClicks(host.current, diagram.ids, onNodeClick);
      })
      .catch((e: Error) => live && setError(e.message));
    return () => {
      live = false;
    };
  }, [diagram, theme, baseId, onNodeClick]);

  useEffect(() => setView({ x: 0, y: 0, scale: 1 }), [diagram]);

  return (
    <div className="diagram">
      <div className="diagram-tools">
        <button onClick={() => setView((v) => ({ ...v, scale: v.scale * 1.25 }))} title="Zoom in">+</button>
        <button onClick={() => setView((v) => ({ ...v, scale: v.scale / 1.25 }))} title="Zoom out">−</button>
        <button onClick={() => setView({ x: 0, y: 0, scale: 1 })} title="Reset view">⟲</button>
      </div>
      {error && <pre className="error">Diagram error: {error}</pre>}
      <div
        className="diagram-viewport"
        onWheel={(e) => {
          if (!e.ctrlKey && !e.metaKey) return;
          e.preventDefault();
          const factor = e.deltaY < 0 ? 1.1 : 1 / 1.1;
          setView((v) => ({ ...v, scale: Math.min(8, Math.max(0.1, v.scale * factor)) }));
        }}
        onPointerDown={(e) => {
          if ((e.target as Element).closest(".icb-clickable")) return;
          drag.current = { x: e.clientX - view.x, y: e.clientY - view.y };
          (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
        }}
        onPointerMove={(e) => {
          if (drag.current) setView((v) => ({ ...v, x: e.clientX - drag.current!.x, y: e.clientY - drag.current!.y }));
        }}
        onPointerUp={() => (drag.current = null)}
      >
        <div
          ref={host}
          className="diagram-canvas"
          style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.scale})` }}
        />
      </div>
    </div>
  );
}

// bindClicks finds the rendered element of each mapped Mermaid node: its
// DOM id contains the Mermaid id between separators (flowchart-r3-12,
// classId-notes_Service-0) or equals it.
function bindClicks(root: HTMLElement, ids: Record<string, string>, onNodeClick?: (graphId: string) => void) {
  if (!onNodeClick) return;
  const keys = Object.keys(ids);
  root.querySelectorAll<SVGGElement>("g[id]").forEach((el) => {
    const key = keys.find((k) => el.id === k || el.id.includes(`-${k}-`) || el.id.endsWith(`-${k}`));
    if (!key) return;
    el.classList.add("icb-clickable");
    el.addEventListener("click", (e) => {
      e.stopPropagation();
      onNodeClick(ids[key]);
    });
  });
}
