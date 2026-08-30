import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import { tanstackStart } from "@tanstack/react-start/plugin/vite";
import react from "@vitejs/plugin-react";
import mdx from "fumadocs-mdx/vite";
import { defineConfig } from "vite";
import tsConfigPaths from "vite-tsconfig-paths";

const __dirname = fileURLToPath(new URL(".", import.meta.url));

const allowedHosts = process.env.VITE_ALLOWED_HOSTS
  ? process.env.VITE_ALLOWED_HOSTS.split(",")
      .map((host) => host.trim())
      .filter(Boolean)
  : true;

export default defineConfig({
  server: {
    port: 3000,
    allowedHosts,
  },
  resolve: {
    alias: {
      // fumadocs-core/source uses node:path in the client bundle —
      // provide a browser shim with the handful of fns it needs.
      "node:path": resolve(__dirname, "src/lib/path-shim.ts"),
    },
  },
  optimizeDeps: {
    include: ["use-sync-external-store/shim/with-selector"],
  },
  plugins: [
    mdx(await import("./source.config")),
    tailwindcss(),
    tsConfigPaths({
      projects: ["./tsconfig.json"],
    }),
    tanstackStart({
      prerender: {
        enabled: true,
      },
    }),
    react(),
  ],
});
