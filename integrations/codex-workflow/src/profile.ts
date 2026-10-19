import { realpath } from 'node:fs/promises'
import { isAbsolute } from 'node:path'

/** Compare directories without changing the profile used to derive pairing IDs. */
export async function sameProfile(a: string, b: string): Promise<boolean> {
  if (typeof a !== 'string' || typeof b !== 'string' || !isAbsolute(a) || !isAbsolute(b)) return false
  if (a === b) return true
  try { return await realpath(a) === await realpath(b) }
  catch { return false }
}
