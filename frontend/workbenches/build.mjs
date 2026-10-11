import { execFileSync } from 'node:child_process'
import { cpSync, mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm'
for (const [source, destination] of [['infinite-canvas', 'canvas'], ['image-playground', 'image']]) {
  const cwd = fileURLToPath(new URL(`./${source}/`, import.meta.url))
  const options = {
    cwd,
    stdio: 'inherit',
    shell: process.platform === 'win32',
    env: { ...process.env, VITE_DEFAULT_API_URL: 'https://api.zshai.cc/v1', VITE_BASE: '/playgrounds/canvas/' },
  }
  if (source === 'infinite-canvas') execFileSync(npm, ['run', 'typecheck'], options)
  execFileSync(npm, ['run', 'build'], options)
  const output = fileURLToPath(new URL(`../public/playgrounds/${destination}/`, import.meta.url))
  mkdirSync(output, { recursive: true })
  cpSync(new URL(`./${source}/dist/`, import.meta.url), output, { recursive: true })
  cpSync(new URL(`./${source}/LICENSE`, import.meta.url), `${output}/LICENSE`)
}
