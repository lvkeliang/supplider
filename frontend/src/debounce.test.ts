import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Debouncer, normalizeQuery } from './debounce'

// Pins the TR-03 live-search timing contract without a DOM.
describe('Debouncer', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('runs once after the delay with the latest scheduled callback', () => {
    const d = new Debouncer(300)
    const first = vi.fn()
    const second = vi.fn()
    d.schedule(first)
    vi.advanceTimersByTime(299)
    d.schedule(second) // resets the wait and drops `first`
    vi.advanceTimersByTime(299)
    expect(first).not.toHaveBeenCalled()
    expect(second).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(first).not.toHaveBeenCalled()
    expect(second).toHaveBeenCalledTimes(1)
  })

  it('cancel prevents a pending call', () => {
    const d = new Debouncer(300)
    const fn = vi.fn()
    d.schedule(fn)
    d.cancel()
    vi.advanceTimersByTime(1000)
    expect(fn).not.toHaveBeenCalled()
  })

  it('flush runs immediately and cancels the pending call', () => {
    const d = new Debouncer(300)
    const scheduled = vi.fn()
    const immediate = vi.fn()
    d.schedule(scheduled)
    d.flush(immediate)
    expect(immediate).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1000)
    expect(scheduled).not.toHaveBeenCalled()
  })

  it('tracks whether a call is pending', () => {
    const d = new Debouncer(300)
    expect(d.pending).toBe(false)
    d.schedule(vi.fn())
    expect(d.pending).toBe(true)
    d.cancel()
    expect(d.pending).toBe(false)
  })
})

describe('normalizeQuery', () => {
  it('trims and maps blank input to undefined (no keyword filter)', () => {
    expect(normalizeQuery('杭州混凝土')).toBe('杭州混凝土')
    expect(normalizeQuery('  杭州  ')).toBe('杭州')
    expect(normalizeQuery('')).toBeUndefined()
    expect(normalizeQuery('   ')).toBeUndefined()
  })
})
