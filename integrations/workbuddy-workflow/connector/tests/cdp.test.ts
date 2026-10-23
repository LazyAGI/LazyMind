import { describe, expect, it } from 'vitest'
import { selectWorkBuddyTarget } from '../src/cdp'

const macURL = 'file:///Applications/WorkBuddy.app/Contents/Resources/app.asar/renderer/index.html'
const windowsURL = 'file:///C:/Users/test/AppData/Local/Programs/WorkBuddy/resources/app.asar/renderer/index.html'
const target = (url: string, id: string) => ({ type: 'page', title: 'WorkBuddy', url, id })

describe('WorkBuddy main-window discovery', () => {
  it.each([macURL, windowsURL])('ignores an independent settings window at %s', url => {
    const main = target(url + '?locale=zh-CN', 'main')
    const settings = target(url + '?windowKind=settings&windowAppId=settings&windowPreset=utility', 'settings')
    expect(selectWorkBuddyTarget([settings, main])).toBe(main)
    expect(selectWorkBuddyTarget([main, settings])).toBe(main)
  })

  it('accepts explicitly marked main windows and legacy windowKind', () => {
    const main = target(macURL + '?windowAppId=main', 'main')
    expect(selectWorkBuddyTarget([target(macURL + '?windowKind=settings', 'settings'), main])).toBe(main)
    expect(selectWorkBuddyTarget([target(macURL + '?windowKind=main', 'main')]).id).toBe('main')
  })

  it('rejects ambiguous main windows instead of picking the first', () => {
    expect(() => selectWorkBuddyTarget([target(macURL, 'one'), target(macURL, 'two')])).toThrow('Expected one')
  })

  it('does not promote auxiliary, remote or unrelated targets to the main window', () => {
    for (const url of [macURL + '?windowAppId=settings', macURL + '?windowKind=unknown',
      'https://example.com/renderer/index.html', 'file:///tmp/preview.html', 'invalid']) {
      expect(() => selectWorkBuddyTarget([target(url, 'other')])).toThrow('Expected one')
    }
    expect(() => selectWorkBuddyTarget([{ ...target(macURL, 'worker'), type: 'worker' }])).toThrow('Expected one')
  })
})
