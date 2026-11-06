#!/usr/bin/env node

// Guards the z-index scale: every stacking value must come from a --z-* token,
// whether it is declared in CSS or inline in a JSX style object. Raw numbers
// silently escape the scale and make later layers impossible to reason about.

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(scriptDir, "..");
const srcRoot = path.join(frontendRoot, "src");
const allowMarker = "z-index-token-allow";

const cssDeclaration = /z-index\s*:\s*([^;}\n]+)/g;
const tsxInlineValue = /\bzIndex\s*:\s*([^\n]+)/g;
const tsxStyleText = /z-index\s*:\s*([^;"'\n]+)/g;
const tokenValue = /^var\(\s*--z-[a-z0-9-]+\s*\)$/;
const numericValue = /\b\d+\b/;

function lineNumber(source, index) {
  return source.slice(0, index).split("\n").length;
}

function isExempt(source, index) {
  const lineStart = source.lastIndexOf("\n", index) + 1;
  let previous = lineStart;
  for (let step = 0; step < 2 && previous > 0; step += 1) {
    const newline = source.lastIndexOf("\n", previous - 2);
    previous = newline > 0 ? newline + 1 : 0;
  }
  return source.slice(previous, index).includes(allowMarker);
}

function isInBlockComment(source, index) {
  const open = source.lastIndexOf("/*", index);
  const close = source.lastIndexOf("*/", index);
  return open > close;
}

// Reads one JS expression starting at `start` and stops at the first top-level
// comma or closing brace, so later properties in the same object literal are
// not mistaken for part of this value.
function expressionAt(source, start) {
  let depth = 0;
  let index = start;
  for (; index < source.length; index += 1) {
    const char = source[index];
    if (char === '"' || char === "'" || char === "`") {
      const quote = char;
      index += 1;
      while (index < source.length && source[index] !== quote) {
        if (source[index] === "\\") index += 1;
        index += 1;
      }
      continue;
    }
    if (char === "(" || char === "[" || char === "{") depth += 1;
    else if (char === ")" || char === "]") {
      if (depth === 0) break;
      depth -= 1;
    } else if (char === "}") {
      if (depth === 0) break;
      depth -= 1;
    } else if (char === "," && depth === 0) break;
  }
  return source.slice(start, index).trim();
}

function inspectExpression(value) {
  const literal = value.match(/^[`"']([^`"']*)[`"']$/);
  if (literal) return tokenValue.test(literal[1].trim()) ? null : literal[1].trim();
  const numeric = value.match(/\b\d+\b/);
  return numeric ? numeric[0] : null;
}

function checkSource(source, file) {
  const errors = [];
  if (file.endsWith(".css")) {
    for (const match of source.matchAll(cssDeclaration)) {
      const value = match[1].trim().replace(/\s+/g, " ");
      if (tokenValue.test(value) || isExempt(source, match.index)) continue;
      errors.push(`${file}:${lineNumber(source, match.index)}: z-index must use a --z-* token, got ${value}`);
    }
    return errors;
  }
  for (const match of source.matchAll(tsxInlineValue)) {
    if (isInBlockComment(source, match.index)) continue;
    const value = expressionAt(source, match.index + match[0].indexOf(":") + 1);
    const offending = inspectExpression(value);
    if (!offending || isExempt(source, match.index)) continue;
    errors.push(`${file}:${lineNumber(source, match.index)}: zIndex must use a --z-* token, got ${offending}`);
  }
  for (const match of source.matchAll(tsxStyleText)) {
    if (isInBlockComment(source, match.index)) continue;
    const value = match[1].trim().replace(/\s+/g, " ");
    if (tokenValue.test(value) || isExempt(source, match.index)) continue;
    errors.push(`${file}:${lineNumber(source, match.index)}: inline z-index must use a --z-* token, got ${value}`);
  }
  return errors;
}

function listFiles(directory, extension) {
  const files = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const target = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...listFiles(target, extension));
    else if (entry.isFile() && target.endsWith(extension)) files.push(target);
  }
  return files.sort();
}

function runSelfTest() {
  const cases = [
    { name: "css token allowed", source: ".a { z-index: var(--z-modal); }", expectError: false, css: true },
    { name: "css number rejected", source: ".a { z-index: 3; }", expectError: true, css: true },
    { name: "tsx token allowed", source: '<div style={{ zIndex: "var(--z-popover)" }} />', expectError: false },
    { name: "tsx number rejected", source: "<div style={{ zIndex: 60 }} />", expectError: true },
    { name: "tsx ternary number rejected", source: "<div style={{ zIndex: dragging ? 5 : undefined }} />", expectError: true },
    { name: "tsx sibling property ignored", source: '<div style={{ zIndex: "var(--z-toast)", width: 60 }} />', expectError: false },
    { name: "tsx style text rejected", source: 'notice.style.cssText = "position:fixed;z-index:2147483647;";', expectError: true },
    { name: "marker exempts a raw value", source: "// z-index-token-allow: stylesheet may be unavailable\nnotice.style.cssText = \"z-index:2147483647;\";", expectError: false },
  ];
  const failures = [];
  for (const testCase of cases) {
    const errors = checkSource(testCase.source, testCase.css ? "fixture.css" : "fixture.tsx");
    if (errors.length > 0 !== testCase.expectError) failures.push(testCase.name);
  }
  if (failures.length > 0) {
    console.error(`z-index gate self-test failed: ${failures.join(", ")}`);
    process.exit(1);
  }
  console.log(`z-index gate self-test passed (${cases.length} cases).`);
}

if (process.argv.includes("--self-test")) {
  runSelfTest();
  process.exit(0);
}

const explicit = process.argv.slice(2);
const files = explicit.length > 0
  ? explicit.map((file) => path.resolve(frontendRoot, file))
  : [...listFiles(srcRoot, ".css"), ...listFiles(srcRoot, ".tsx")];

const errors = [];
for (const fullPath of files) {
  const source = fs.readFileSync(fullPath, "utf8");
  errors.push(...checkSource(source, path.relative(frontendRoot, fullPath).replace(/\\/g, "/")));
}

if (errors.length > 0) {
  for (const error of errors) console.error(error);
  process.exit(1);
}

console.log(`z-index token check passed: ${files.length} files (${files.filter((file) => file.endsWith(".css")).length} CSS, ${files.filter((file) => file.endsWith(".tsx")).length} TSX).`);
