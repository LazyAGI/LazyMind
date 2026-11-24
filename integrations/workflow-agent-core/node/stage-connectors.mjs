import { createHash } from 'node:crypto'
import { mkdir, readFile, readdir, rename, rm, writeFile } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const bin = process.argv[2]
if (!bin || process.argv.length !== 3) throw new Error('Usage: node stage-connectors.mjs <CLI bin directory>')
const integrations = fileURLToPath(new URL('../../', import.meta.url))
const dest = resolve(bin, 'workflow-connectors')
const temporary = dest + '.staging-' + process.pid
await mkdir(temporary, { recursive: true })
try {
  for (const [host, directory] of Object.entries({
    codex: 'codex-workflow',
    workbuddy: 'workbuddy-workflow/connector',
  })) {
    const source = join(integrations, directory)
    const runtime = join(source, 'dist/connector')
    const files = (await readdir(runtime, { recursive: true }))
      .map(file => file.replaceAll('\\', '/')).filter(file => file.endsWith('.mjs')).sort()
    const entry = 'main.mjs', presentation = 'presentation.mjs', control = 'adapter.mjs'
    for (const file of [entry, presentation, control]) {
      if (!files.includes(file)) throw new Error('Missing ' + host + ' Connector module: ' + join(runtime, file))
    }
    const hash = createHash('sha256')
    for (const file of files) {
      const data = await readFile(join(runtime, file))
      hash.update(file).update('\0').update(createHash('sha256').update(data).digest())
      const target = join(temporary, host, file)
      await mkdir(dirname(target), { recursive: true })
      await writeFile(target, data)
    }
    const release = hash.digest('hex')
    const { version } = JSON.parse(await readFile(join(source, 'package.json'), 'utf8'))
    const manifest = { schema: 1, host, release, version: version + '+' + release.slice(0, 16), entry, presentation, control, files }
    await writeFile(join(temporary, host, 'connector.json'), JSON.stringify(manifest) + '\n')
  }
  await rm(dest, { recursive: true, force: true })
  await rename(temporary, dest)
} finally {
  await rm(temporary, { recursive: true, force: true })
}
