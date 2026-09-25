import { copyFile, mkdir } from 'node:fs/promises'
const destination = new URL('../../../local/lazymind-cli/internal/codexplugin/assets/dispatcher.mjs', import.meta.url)
await mkdir(new URL('.', destination), { recursive: true })
await copyFile(new URL('../dist/main.mjs', import.meta.url), destination)
