import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    pool: "forks",
    globals: true,
    include: ["tests/**/*.test.ts"],
  },
});
