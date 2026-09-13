// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { resolveDark } from './theme'

// TR-17 暗色模式: the persisted choice wins over the OS preference; with
// nothing persisted resolveDark() follows prefers-color-scheme (mirroring the
// inline no-flash script in index.html).

function stubMatchMedia(matches: boolean) {
  vi.stubGlobal(
    'matchMedia',
    (_query: string) => ({
      matches,
      addEventListener: () => {},
      removeEventListener: () => {},
    }),
  )
}

describe('resolveDark', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => vi.unstubAllGlobals())

  it('prefers a persisted choice over the OS preference', () => {
    stubMatchMedia(true) // OS prefers dark, but the user chose light
    localStorage.setItem('supplider.theme', 'light')
    expect(resolveDark()).toBe(false)

    localStorage.setItem('supplider.theme', 'dark')
    expect(resolveDark()).toBe(true)
  })

  it('falls back to the OS preference when nothing is persisted', () => {
    stubMatchMedia(false)
    expect(resolveDark()).toBe(false)

    stubMatchMedia(true)
    expect(resolveDark()).toBe(true)
  })

  it('returns false when localStorage is unavailable (private mode)', () => {
    localStorage.getItem = () => {
      throw new Error('denied')
    }
    stubMatchMedia(false)
    expect(resolveDark()).toBe(false)
  })
})