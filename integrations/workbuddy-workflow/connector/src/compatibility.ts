// Serialized into the renderer as an explicit argument; keep self-contained.
export function workbuddyVersionIssue(version: unknown): string {
  if (typeof version !== 'string') return 'version_unreadable'
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.exec(version)
  if (!match || match[4]?.split('.').some(part => /^0\d+$/.test(part))) return 'version_unreadable'
  if (match.slice(1, 4).some(part => !Number.isSafeInteger(Number(part)))) return 'version_unreadable'
  const minimum = [5, 8, 3]
  for (let i = 0; i < minimum.length; i++) {
    const value = Number(match[i + 1])
    if (!Number.isSafeInteger(value)) return 'version_unreadable'
    if (value !== minimum[i]) return value > minimum[i] ? '' : 'unsupported_version'
  }
  return match[4] ? 'unsupported_version' : ''
}
