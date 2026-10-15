import { defineConfig } from 'vitest/config'
import { resolve } from 'path'

export default defineConfig({
  resolve: { alias: {
    '@': resolve(__dirname, 'workbenches/infinite-canvas/src'),
    'axios': resolve(__dirname, 'node_modules/axios/index.js'),
  } },
  test: { environment: 'node', include: ['workbenches/infinite-canvas/tests/**/*.test.ts'] },
})
