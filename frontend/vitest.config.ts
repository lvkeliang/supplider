import { defineConfig } from 'vitest/config'

// Node-environment unit tests for pure frontend logic (URL/query
// serialization, mappers). Component/DOM tests can opt into jsdom later.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
})
