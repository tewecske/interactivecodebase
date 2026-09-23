import { createContext, useEffect, useState } from "react";

// AnalysisStatus mirrors live.Status on the server.
export interface AnalysisStatus {
  generation: number;
  analyzing: boolean;
  updatedAt: string;
  durationMs: number;
  error?: string;
}

// Generation is the analysis generation the views show; data hooks reload
// when it changes.
export const Generation = createContext(0);

// useAnalysisStatus follows the server's analysis status over server-sent
// events. EventSource reconnects on its own after a dropped connection.
export function useAnalysisStatus(): AnalysisStatus | undefined {
  const [status, setStatus] = useState<AnalysisStatus>();
  useEffect(() => {
    if (typeof EventSource === "undefined") return;
    const es = new EventSource("api/events");
    es.addEventListener("status", (e) =>
      setStatus(JSON.parse((e as MessageEvent<string>).data) as AnalysisStatus),
    );
    return () => es.close();
  }, []);
  return status;
}

export function StatusIndicator({ status }: { status?: AnalysisStatus }) {
  if (!status) return null;
  let cls = "ok";
  let text = `analysis #${status.generation}`;
  let title = `Analyzed in ${(status.durationMs / 1000).toFixed(1)}s at ${new Date(status.updatedAt).toLocaleTimeString()}`;
  if (status.analyzing) {
    cls = "busy";
    text = "re-analyzing…";
    title =
      "Source changed; showing the previous analysis until the new one is ready";
  } else if (status.error) {
    cls = "error";
    text = "analysis failed";
    title = `Showing analysis #${status.generation}; the latest re-analysis failed:\n${status.error}`;
  }
  return (
    <span className={`status ${cls}`} title={title} role="status">
      <span className="dot" />
      {text}
    </span>
  );
}
