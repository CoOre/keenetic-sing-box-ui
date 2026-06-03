import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// Build the SPA into web/dist, which the Go backend embeds via //go:embed.
// base: "/" because the backend serves assets at the site root.
export default defineConfig({
  plugins: [svelte()],
  base: "/",
  build: {
    outDir: "dist",
    emptyOutDir: true,
    target: "es2020",
    chunkSizeWarningLimit: 800,
  },
  server: {
    proxy: {
      // During `npm run dev`, forward API calls to a locally running backend.
      "/api": "http://127.0.0.1:9091",
      "/healthz": "http://127.0.0.1:9091",
    },
  },
});
