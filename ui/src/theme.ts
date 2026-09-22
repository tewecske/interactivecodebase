// Light/dark theme: follows the system unless the user picks one.
import { useEffect, useState } from "react";

export type Theme = "light" | "dark";

const media = window.matchMedia?.("(prefers-color-scheme: dark)");

function stored(): Theme | null {
  try {
    const t = localStorage.getItem("icb-theme");
    return t === "light" || t === "dark" ? t : null;
  } catch {
    return null;
  }
}

export function useTheme(): [Theme, () => void] {
  const [choice, setChoice] = useState<Theme | null>(stored);
  const [system, setSystem] = useState<Theme>(media?.matches ? "dark" : "light");
  useEffect(() => {
    const onChange = (e: MediaQueryListEvent) => setSystem(e.matches ? "dark" : "light");
    media?.addEventListener("change", onChange);
    return () => media?.removeEventListener("change", onChange);
  }, []);
  const theme = choice ?? system;
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);
  const toggle = () => {
    const next: Theme = theme === "dark" ? "light" : "dark";
    setChoice(next);
    try {
      localStorage.setItem("icb-theme", next);
    } catch {
      // private mode: the choice lasts for this page only
    }
  };
  return [theme, toggle];
}
