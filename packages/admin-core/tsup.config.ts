import { defineConfig, Options } from "tsup";

export default defineConfig((options: Options) => ({
  entry: {
    index: "src/index.ts",
    "components/index": "src/components/index.ts",
    "hooks/index": "src/hooks/index.ts",
    "server/index": "src/server/index.ts",
    "stores/index": "src/stores/index.ts",
    "lib/index": "src/lib/index.ts",
  },
  format: ["esm", "cjs"],
  dts: true,
  splitting: true,
  treeshake: true,
  clean: true,
  sourcemap: true,
  external: [
    "react",
    "react-dom",
    "@everstack/ui",
    "@everstack/proto",
    "@everstack/client",
    "@everstack/utils",
  ],
  ...options,
}));



