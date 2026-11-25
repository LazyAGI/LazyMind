import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, readdir, rename, rm, writeFile } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { dshBuildVersion } from '../../dsh-workflow/release-version.mjs'

const [, , bin, mode, ...extra] = process.argv
if (!bin || (mode && mode !== '--dsh-only') || extra.length || !process.env.npm_execpath) {
  throw new Error('Usage: pnpm --dir integrations/dsh-workflow stage-connectors <absolute CLI bin directory> [--dsh-only]')
}
const integrations = fileURLToPath(new URL('../../', import.meta.url))
const dest = resolve(bin, 'workflow-connectors')
const temporary = dest + '.staging-' + process.pid
await mkdir(temporary, { recursive: true })
try {
  const desktop = mode ? {} : { codex: 'codex-workflow', workbuddy: 'workbuddy-workflow/connector' }
  for (const [host, directory] of Object.entries(desktop)) {
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
  const dsh = join(integrations, 'dsh-workflow')
  const pack = join(temporary, 'pack')
  await mkdir(pack)
  for (const file of ['lib/index.js', 'lib/client.js']) {
    const compiled = await readFile(join(dsh, file), 'utf8')
    if (!compiled.includes(JSON.stringify(dshBuildVersion))) throw new Error('Rebuild the DSH Connector before staging: ' + file)
  }
  execFileSync(process.execPath, [process.env.npm_execpath, 'pack', '--pack-destination', pack], { cwd: dsh, stdio: 'inherit' })
  const [archive, ...other] = await readdir(pack)
  if (!archive?.endsWith('.tgz') || other.length) throw new Error('Expected one DSH plugin archive')
  const entry = 'dsh-workflow.tgz'
  const data = await readFile(join(pack, archive))
  const release = createHash('sha256').update(entry).update('\0').update(createHash('sha256').update(data).digest()).digest('hex')
  const manifest = { schema: 1, host: 'dsh', release, version: dshBuildVersion, entry, files: [entry] }
  await mkdir(join(temporary, 'dsh'))
  await writeFile(join(temporary, 'dsh', entry), data)
  await writeFile(join(temporary, 'dsh', 'connector.json'), JSON.stringify(manifest) + '\n')
  await rm(pack, { recursive: true })
  if (mode) {
    await mkdir(dest, { recursive: true })
    await rm(join(dest, 'dsh'), { recursive: true, force: true })
    await rename(join(temporary, 'dsh'), join(dest, 'dsh'))
  } else {
    await rm(dest, { recursive: true, force: true })
    await rename(temporary, dest)
  }
} finally {
  await rm(temporary, { recursive: true, force: true })
}
