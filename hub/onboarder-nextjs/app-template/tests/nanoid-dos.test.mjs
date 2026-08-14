import { test } from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import fs from 'node:fs'
import { customAlphabet as nonSecureCustomAlphabet } from 'nanoid/non-secure'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.join(__dirname, '..')

// Hang detector, not a performance budget: the vulnerable call never returns
// at all, so any generous timeout discriminates fixed-vs-vulnerable cleanly.
const HANG_TIMEOUT_MS = 3000

test('nanoid resolves to a version clearing GHSA-2v37-7h3g-55p8 (>= 3.3.18)', () => {
  const pkg = JSON.parse(
    fs.readFileSync(path.join(ROOT, 'node_modules', 'nanoid', 'package.json'), 'utf8')
  )
  const [major, minor, patch] = pkg.version.split('.').map(Number)
  assert.ok(
    major > 3 || (major === 3 && (minor > 3 || (minor === 3 && patch >= 18))),
    `nanoid ${pkg.version} is below the patched floor 3.3.18`
  )
})

test("postcss's actual call site (nanoid/non-secure customAlphabet, fixed size 6) still works after the bump", () => {
  // postcss/lib/input.js does `require('nanoid/non-secure')` and always calls
  // the generator with a fixed size of 6 -- this repo's only reachable nanoid
  // entry point, and one the GHSA never touched (its counting loop already
  // terminates for size 0 in every version). This proves the bump didn't
  // break the call site production actually reaches.
  const id = nonSecureCustomAlphabet('abcdefghijklmnopqrstuvwxyz', 6)()
  assert.equal(id.length, 6)
})

test('the vulnerable surface (secure customAlphabet/customRandom, size 0) returns immediately instead of hanging', () => {
  // GHSA-2v37-7h3g-55p8 / CVE-2026-67213: the crypto-backed "nanoid" build
  // (not the "non-secure" one postcss uses) looped forever on size 0. Nothing
  // in this repo's tree calls it that way today, but Dependabot flags the
  // package version regardless of call site, so this proves the shipped fix
  // itself works rather than trusting the version number alone. Spawned
  // out-of-process because the bug is a synchronous busy loop that would
  // otherwise hang the test runner's own thread.
  const fixture = path.join(__dirname, 'fixtures', 'nanoid-size-zero.mjs')
  const result = spawnSync(process.execPath, [fixture], {
    cwd: ROOT,
    timeout: HANG_TIMEOUT_MS,
    encoding: 'utf8',
  })

  assert.notEqual(
    result.signal,
    'SIGTERM',
    'the size=0 call hung past the timeout (vulnerable nanoid version)'
  )
  assert.equal(result.status, 0, `fixture exited non-zero: ${result.stderr}`)
  const output = JSON.parse(result.stdout)
  assert.equal(output.result, '', 'expected an empty string, not a hang, for size 0')
})
