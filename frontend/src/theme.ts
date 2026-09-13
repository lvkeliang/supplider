import { useCallback, useEffect, useState } from 'react'

// TR-17 暗色模式: the `.dark` class on <html> drives every `dark:` variant
// (Tailwind `darkMode: 'class'`). This hook resolves the initial theme
// (persisted choice, else the OS preference) and toggles it. The inline
// script in index.html applies the same resolveDark() before the bundle
// loads, so there is no light flash on dark-preference users.

const KEY = 'supplider.theme'

/** Resolve whether dark mode should be on (persisted choice → OS preference). */
export function resolveDark(): boolean {
  try {
    const s = localStorage.getItem(KEY)
    if (s === 'dark') return true
    if (s === 'light') return false
    return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false
  } catch {
    return false
  }
}

/** useDarkMode returns [isDark, toggle]. The class is applied to <html>. */
export function useDarkMode(): [boolean, () => void] {
  const [dark, setDark] = useState<boolean>(resolveDark)

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    try {
      localStorage.setItem(KEY, dark ? 'dark' : 'light')
    } catch {
      /* localStorage unavailable (private mode) — non-persistent, fine */
    }
  }, [dark])

  const toggle = useCallback(() => setDark((d) => !d), [])
  return [dark, toggle]
}
