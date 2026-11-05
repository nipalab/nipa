import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";
import { fileURLToPath } from "node:url";

const src = (p: string) => fileURLToPath(new URL(p, import.meta.url));
const demo = process.env.NIPA_DEMO === "1";

export default defineConfig({
  resolve: {
    alias: [
      { find: "@", replacement: src("./src") },
      ...(demo
        ? [
            { find: /^(\.\.\/)+bindings\/github\.com\/nipalab\/nipa\/desktop\/internal\/service$/, replacement: src("./src/demo/index.ts") },
            { find: "@wailsio/runtime", replacement: src("./src/demo/runtime.ts") },
          ]
        : []),
    ],
  },
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  build: { chunkSizeWarningLimit: 1024 },
  plugins: [react(), tailwindcss(), ...(demo ? [] : [wails("./bindings")])],
});
