import { defineConfig } from "tsup";

export default defineConfig({
  entry: {
    index: "src/index.ts",
    "adapters/vercel": "src/adapters/vercel.ts",
    "adapters/express": "src/adapters/express.ts",
    "adapters/fastify": "src/adapters/fastify.ts",
    "adapters/nest": "src/adapters/nest.ts",
    "adapters/koa": "src/adapters/koa.ts",
  },
  format: ["esm", "cjs"],
  dts: true,
  clean: true,
  splitting: false,
  sourcemap: true,
});
