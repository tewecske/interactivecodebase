import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./styles.css";

// "/" focuses the global search, like most code browsers.
window.addEventListener("keydown", (e) => {
  const target = e.target as HTMLElement;
  if (e.key === "/" && !["INPUT", "TEXTAREA"].includes(target.tagName)) {
    e.preventDefault();
    document.getElementById("global-search")?.focus();
  }
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
