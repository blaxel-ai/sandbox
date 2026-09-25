import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// sharp is not imported by this template; Next.js loads it as its optional
// image optimizer, and `overrides.sharp` in package.json pins the copy Next
// resolves. GHSA-rgj7-g3m4-5g8c is fixed by the libheif bundled in sharp
// 0.35.4 (libheif 1.23.2), so this loads sharp the way Next does (resolved
// from next's own directory) and checks the native binary actually carries
// that libheif, then runs a real encode/resize/decode round trip so a broken
// prebuilt binary fails here rather than on the first optimized image.

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.join(__dirname, '..')
const require = createRequire(import.meta.url)
const nextDir = path.dirname(require.resolve('next/package.json', { paths: [ROOT] }))
const sharp = require(require.resolve('sharp', { paths: [nextDir] }))

function atLeast(version, floor) {
  const a = String(version).split('.').map(Number)
  const b = floor.split('.').map(Number)
  for (let i = 0; i < b.length; i++) {
    if ((a[i] || 0) > b[i]) return true
    if ((a[i] || 0) < b[i]) return false
  }
  return true
}

test('the sharp binary Next resolves bundles a libheif clearing GHSA-rgj7-g3m4-5g8c', () => {
  const heif = sharp.versions.heif
  assert.ok(heif, 'sharp.versions.heif is missing: the prebuilt libvips did not load')
  assert.ok(atLeast(heif, '1.23.2'), `sharp ${sharp.versions.sharp} bundles libheif ${heif}, below 1.23.2`)
})

test('sharp encodes, resizes and decodes an image end to end', async () => {
  const png = await sharp({ create: { width: 8, height: 6, channels: 3, background: { r: 10, g: 120, b: 200 } } })
    .png()
    .toBuffer()
  const resized = await sharp(png).resize(4, 3).png().toBuffer()
  const meta = await sharp(resized).metadata()
  assert.equal(meta.width, 4)
  assert.equal(meta.height, 3)
  assert.equal(meta.format, 'png')
})
