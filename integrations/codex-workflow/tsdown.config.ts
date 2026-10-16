import { defineConfig } from 'tsdown'

// The Go-embedded dispatcher runs outside node_modules in a released desktop.
export default defineConfig({ noExternal: ['ws'], shims: true })
