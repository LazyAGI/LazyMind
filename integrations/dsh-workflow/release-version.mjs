import { createHash } from 'node:crypto'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('.', import.meta.url))
const files = ['release-version.mjs', 'package.json', 'pnpm-lock.yaml', 'tsconfig.json', 'tsdown.config.ts', 'cordis.patch.yml']
for (const directory of ['src', '../workflow-agent-core/src', '../workflow-agent-core/node', '../workflow-agent-core/renderer']) {
  for (const file of readdirSync(join(root, directory), { recursive: true })) {
    if (/\.tsx?$/.test(file)) files.push(directory + '/' + file.replaceAll('\\', '/'))
  }
}

const hash = createHash('sha256')
for (const name of files.sort()) {
  const content = readFileSync(join(root, name), 'utf8').replaceAll('\r\n', '\n')
  hash.update(name).update('\0').update(createHash('sha256').update(content).digest())
}

export const dshBuildVersion = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version + '+' + hash.digest('hex').slice(0, 16)
