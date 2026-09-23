import { useEffect, useState } from "react";
import { api } from "../api";
import { Loaded } from "../components/Common";
import { flatOrder, go, kindLabels, ResultList } from "../components/Search";
import { navigate } from "../router";
import { useAsync } from "../useAsync";

// SearchView lists all results for q (and/or kind), grouped by kind, with
// ↑/↓ and Enter to open.
export function SearchView({ params }: { params: Record<string, string> }) {
  const { q = "", kind = "" } = params;
  const results = useAsync(() => (q || kind ? api.search(q, kind || undefined, 300).then(flatOrder) : Promise.resolve([])), [q, kind]);
  const [active, setActive] = useState(-1);
  useEffect(() => setActive(-1), [q, kind]);
  useEffect(() => {
    if (results.loading || !("data" in results)) return;
    const list = results.data;
    const onKey = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).tagName === "INPUT" || (e.target as HTMLElement).tagName === "SELECT") return;
      if (e.key === "ArrowDown") setActive((a) => Math.min(a + 1, list.length - 1));
      else if (e.key === "ArrowUp") setActive((a) => Math.max(a - 1, 0));
      else if (e.key === "Enter" && active >= 0 && list[active]) go(list[active]);
      else return;
      e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [results, active]);
  useEffect(() => {
    document.querySelector(`.result[data-index="${active}"]`)?.scrollIntoView?.({ block: "nearest" });
  }, [active]);

  return (
    <section>
      <h1>{q ? <>Search: “{q}”</> : "Search"}</h1>
      <div className="toolbar">
        <label>
          kind{" "}
          <select value={kind} onChange={(e) => navigate("search", { q: q || undefined, kind: e.target.value || undefined })}>
            <option value="">all</option>
            {Object.entries(kindLabels).map(([k, label]) => (
              <option key={k} value={k}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <span className="muted">↑/↓ to move, Enter to open, / to search again</span>
      </div>
      <Loaded state={results}>
        {(list) =>
          list.length === 0 ? (
            <p className="muted">{q || kind ? "Nothing matches." : "Type in the search box above."}</p>
          ) : (
            <>
              <p className="muted">{list.length} results</p>
              <ResultList nodes={list} active={active} onPick={go} />
            </>
          )
        }
      </Loaded>
    </section>
  );
}
