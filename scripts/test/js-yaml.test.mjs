// Regression test for the js-yaml override (GHSA-52cp-r559-cp3m, GHSA-5p4m-2wfm-xmqj,
// GHSA-2883-xcg3-v3hh, all fixed by 4.3.2). js-yaml reaches this project only through
// @blaxel/core -> @hey-api/json-schema-ref-parser, which pins `js-yaml: 4.2.0` exactly, so package.json
// overrides it. The module is loaded through that same consumer so the test exercises the
// copy the override actually puts in front of it.
import assert from "node:assert/strict";
import { existsSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
import test from "node:test";

const requireFrom = (...chain) => {
  let req = createRequire(import.meta.url);
  for (const pkg of chain.slice(0, -1)) {
    const dir = (req.resolve.paths(pkg) ?? []).map((base) => join(base, pkg)).find((c) => existsSync(join(c, "package.json")));
    assert.ok(dir, `cannot resolve ${pkg}`);
    req = createRequire(join(realpathSync(dir), "package.json"));
  }
  return req(chain.at(-1));
};

test("js-yaml enforces the merge-key budget for empty merge sources", () => {
  const yaml = requireFrom("@hey-api/json-schema-ref-parser", "js-yaml");
  // A long merge sequence of empty mappings: 4.2.0 rejects it with a different error
  // and 4.3.1 accepts it, so this fails on both vulnerable lines. On 4.3.2
  // the merge sequence cap fires first, so it does not isolate the empty-merge budget.
  const n = 2_000;
  const doc = `arr: &arr [${"{},".repeat(n).slice(0, -1)}]\ntargets:\n${"  - <<: *arr\n".repeat(n)}`;
  assert.throws(() => yaml.load(doc), /abnormal merge sequence size/i);
});
