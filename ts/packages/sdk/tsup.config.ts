import { defineConfig } from "tsup";

export default defineConfig({
  entry: ["src/index.ts", "src/express/index.ts", "src/fastify/index.ts", "src/hono/index.ts"],
  format: ["esm", "cjs"],
  dts: true,
  clean: true,
  splitting: false,
  sourcemap: true,
});
