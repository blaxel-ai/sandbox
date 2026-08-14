// Run out-of-process by tests/nanoid-dos.test.mjs under a timeout: on the
// vulnerable nanoid build (< 3.3.18) this call never returns (GHSA-2v37-7h3g-55p8).
import { customAlphabet } from 'nanoid'

const gen = customAlphabet('abcdefghijklmnopqrstuvwxyz', 10)
const result = gen(0)

process.stdout.write(JSON.stringify({ result }))
