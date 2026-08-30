import { defineConfig, type Options } from "tsup";

export default defineConfig((options: Options) => ({
    entry: ["src/index.ts", "src/config.ts"],
    format: ["esm", "cjs"],
    dts: true,
    clean: true,
    sourcemap: true,
    splitting: true,
    treeshake: true,
    minify: false,
    noExternal: ["@everstack/proto"],
    ...options,
}));
