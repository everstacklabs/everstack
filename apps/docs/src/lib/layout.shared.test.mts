import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const layoutSource = readFileSync(
  new URL("./layout.shared.tsx", import.meta.url),
  "utf8",
);

test("the docs navigation bounds the logo mark explicitly", () => {
  assert.match(layoutSource, /style=\{\{\s*width: 23,\s*height: 24,?\s*\}\}/);
  assert.doesNotMatch(layoutSource, /inline-grid/);
  assert.equal(
    layoutSource.match(/absolute inset-0 size-full object-contain/g)?.length,
    2,
  );
});
