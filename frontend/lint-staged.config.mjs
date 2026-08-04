// `vitest related` walks each test file's full dependency graph and re-transforms
// every import (including .scss/.css/.png/.svg/.md) as if it were a JS module.
// That crashes with a Rollup parse error whenever a related test imports a
// static asset (see https://github.com/vitest-dev/vitest/issues/7437), which
// is unavoidable given how many components import images/styles here.
//
// Instead, resolve each staged file to its co-located `*.test.ts(x)` and run
// `vitest run` directly on that fixed list of test files. This goes through
// the normal Vite plugin pipeline (which handles static assets correctly)
// instead of the buggy dependency-graph walker.
import { existsSync } from 'node:fs';

function testCandidatesFor(file) {
  if (/\.test\.tsx?$/.test(file)) {
    return [file];
  }
  const base = file.replace(/\.tsx?$/, '');
  return ['.test.ts', '.test.tsx'].map((suffix) => `${base}${suffix}`);
}

export default {
  'src/**/*.{ts,tsx}': (files) => {
    const testFiles = new Set();
    for (const file of files) {
      for (const candidate of testCandidatesFor(file)) {
        if (existsSync(candidate)) {
          testFiles.add(candidate);
        }
      }
    }

    if (testFiles.size === 0) {
      return 'echo "No related unit tests found for staged files, skipping"';
    }

    const quoted = [...testFiles].map((f) => JSON.stringify(f)).join(' ');
    return `vitest run ${quoted}`;
  },
};
