import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// Runtime smoke for the Next.js bump (GHSA-2xp9-vwfh-vxw4, GHSA-p293-qw3h-jr36,
// fixed in 16.3.3) and for sharp 0.35.4 through the path production actually
// uses: Next's image optimizer. `next build` proves the app compiles; this
// starts the built app with `next start` (same entry point as the shipped
// image's entrypoint) and fetches real routes, including `/_next/image` for a
// bundled PNG, which is served through sharp.
//
// Requires a prior `next build`; the dependency-verification workflow runs
// the build before `npm test` for that reason.

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.join(__dirname, '..')
const require = createRequire(import.meta.url)
const NEXT_BIN = require.resolve('next/dist/bin/next', { paths: [ROOT] })
const READY_TIMEOUT_MS = 60000

let child
let base
let output = ''

function freePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer()
    server.unref()
    server.on('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address()
      server.close(() => resolve(port))
    })
  })
}

async function waitForServer(url) {
  const deadline = Date.now() + READY_TIMEOUT_MS
  let lastError
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`next start exited early (code ${child.exitCode}):\n${output.slice(-2000)}`)
    }
    try {
      const response = await fetch(url)
      if (response.status === 200) return
      lastError = new Error(`status ${response.status}`)
    } catch (error) {
      lastError = error
    }
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  throw new Error(`server not ready after ${READY_TIMEOUT_MS}ms: ${lastError}\n${output.slice(-2000)}`)
}

before(async () => {
  const port = await freePort()
  base = `http://127.0.0.1:${port}`
  child = spawn(process.execPath, [NEXT_BIN, 'start', '-p', String(port), '-H', '127.0.0.1'], {
    cwd: ROOT,
    stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, NODE_ENV: 'production' },
  })
  child.stdout.on('data', (chunk) => { output += chunk })
  child.stderr.on('data', (chunk) => { output += chunk })
  await waitForServer(base + '/')
})

after(async () => {
  if (!child) return
  child.kill('SIGTERM')
  await new Promise((resolve) => setTimeout(resolve, 500))
  if (child.exitCode === null) child.kill('SIGKILL')
})

test('the built app serves the landing page and the health route', async () => {
  const page = await fetch(base + '/')
  assert.equal(page.status, 200)
  assert.match(await page.text(), /<html/i)

  const health = await fetch(base + '/api/health')
  assert.equal(health.status, 200, `GET /api/health -> ${health.status}`)
})

test('an unknown route is a clean 404, not a server crash', async () => {
  const missing = await fetch(base + '/definitely-not-a-route')
  assert.equal(missing.status, 404)
  assert.equal(child.exitCode, null, 'next start died while serving a 404')
})

test('the image optimizer (sharp) serves a bundled PNG', async () => {
  // public/logo_short.png is shipped with the template; /_next/image runs
  // it through sharp, the library GHSA-rgj7-g3m4-5g8c is about.
  const response = await fetch(base + '/_next/image?url=%2Flogo_short.png&w=64&q=75')
  assert.equal(response.status, 200, `GET /_next/image -> ${response.status}\n${output.slice(-2000)}`)
  const type = response.headers.get('content-type') || ''
  assert.ok(type.startsWith('image/'), `unexpected content-type ${type}`)
  const bytes = new Uint8Array(await response.arrayBuffer())
  assert.ok(bytes.length > 0, 'optimized image is empty')
})
