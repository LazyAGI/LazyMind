#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';

const runtimeRoot = process.argv[2];
if (!runtimeRoot) {
  throw new Error('Usage: write-editable-ppt-dependency-config.mjs <runtime-root>');
}

const entries = {
  windowsX64: {
    url: String(process.env.LAZYMIND_EDITABLE_PPT_WINDOWS_X64_URL || '').trim(),
    sha256: String(process.env.LAZYMIND_EDITABLE_PPT_WINDOWS_X64_SHA256 || '').trim().toLowerCase(),
  },
  darwinArm64: {
    url: String(process.env.LAZYMIND_EDITABLE_PPT_DARWIN_ARM64_URL || '').trim(),
    sha256: String(process.env.LAZYMIND_EDITABLE_PPT_DARWIN_ARM64_SHA256 || '').trim().toLowerCase(),
  },
};

for (const [name, entry] of Object.entries(entries)) {
  if (Boolean(entry.url) !== Boolean(entry.sha256)) {
    throw new Error(`${name} editable PPT dependency requires both URL and SHA256`);
  }
  if (entry.sha256 && !/^[a-f0-9]{64}$/.test(entry.sha256)) {
    throw new Error(`${name} editable PPT dependency SHA256 must contain 64 hexadecimal characters`);
  }
}

const configDir = path.join(path.resolve(runtimeRoot), 'config');
fs.mkdirSync(configDir, { recursive: true });
fs.writeFileSync(
  path.join(configDir, 'editable-ppt-dependencies.json'),
  `${JSON.stringify({ schemaVersion: 1, ...entries }, null, 2)}\n`,
);
