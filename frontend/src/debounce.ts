/**
 * Tiny framework-agnostic debouncer (TR-03): the list search box fires a
 * query 300ms after the user stops typing, while Enter / the 🔍 button / the
 * ✕ clear button commit immediately. Kept out of React so the timing
 * contract (only the latest scheduled call fires; flush runs at once and
 * cancels the pending one) is unit-testable under fake timers.
 */
export class Debouncer {
  private timer: ReturnType<typeof setTimeout> | null = null

  constructor(private readonly delayMs: number) {}

  /** Run fn after delayMs; a later schedule replaces this one. */
  schedule(fn: () => void): void {
    this.cancel()
    this.timer = setTimeout(() => {
      this.timer = null
      fn()
    }, this.delayMs)
  }

  /** Cancel any pending call. */
  cancel(): void {
    if (this.timer !== null) {
      clearTimeout(this.timer)
      this.timer = null
    }
  }

  /** Run fn now and drop the pending call (immediate commit). */
  flush(fn: () => void): void {
    this.cancel()
    fn()
  }

  get pending(): boolean {
    return this.timer !== null
  }
}

/** Trim a search box value; empty/whitespace means "no keyword" (undefined). */
export function normalizeQuery(input: string): string | undefined {
  const v = input.trim()
  return v === '' ? undefined : v
}
