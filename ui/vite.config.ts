import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development, /api is proxied to a running `icb serve`.
export default defineConfig({
  plugins: [react()],
  base: "./",
  // The build lands where the Go binary embeds it (internal/webui).
  build: { outDir: "../internal/webui/dist", emptyOutDir: true, chunkSizeWarningLimit: 4000 },
  server: { proxy: { "/api": "http://127.0.0.1:8080" } },
  test: { environment: "jsdom" },
});
