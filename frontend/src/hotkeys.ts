/**
 * Global keyboard shortcuts (TR-18). Kept as a pure mapper so the rules are
 * unit-testable; App subscribes to window keydown and acts on the result.
 *
 *   Ctrl/⌘+N  → create supplier
 *   Ctrl/⌘+K  or  /  → focus the list search box
 *   Esc       → back to the list
 *
 * Modifier-less keys (/, Esc) are ignored while focus is in an editable
 * control (typing must keep working). Ctrl+N is likewise ignored there so
 * it never abandons an in-progress form; Ctrl+K focuses search even while
 * typing (standard command-palette behaviour).
 */
export type HotkeyAction = 'new' | 'search' | 'back'

/** Dispatched to ask the list view to focus its search input. */
export const FOCUS_SEARCH_EVENT = 'srm:focus-search'

/** Whether the event target is a text-editing control. */
export function isEditableTarget(target: EventTarget | null): boolean {
  if (typeof HTMLElement === 'undefined' || !(target instanceof HTMLElement)) return false
  const tag = target.tagName
  return Boolean(
    tag === 'INPUT' ||
      tag === 'TEXTAREA' ||
      tag === 'SELECT' ||
      target.isContentEditable ||
      // jsdom lacks isContentEditable; the attribute is equivalent for our
      // needs (and a harmless backstop in older webviews).
      target.getAttribute('contenteditable') === 'true',
  )
}

interface KeyLike {
  key: string
  ctrlKey?: boolean
  metaKey?: boolean
  altKey?: boolean
  shiftKey?: boolean
}

/** Map a keyboard event (+target) to an action, or null when it is no shortcut. */
export function hotkeyAction(e: KeyLike, target: EventTarget | null): HotkeyAction | null {
  const key = e.key
  const mod = e.ctrlKey === true || e.metaKey === true
  if (e.altKey) return null

  if (mod && (key === 'n' || key === 'N')) {
    return isEditableTarget(target) ? null : 'new'
  }
  if (mod && (key === 'k' || key === 'K')) {
    return 'search'
  }
  if (!mod && !e.shiftKey && key === '/') {
    return isEditableTarget(target) ? null : 'search'
  }
  if (!mod && key === 'Escape') {
    return isEditableTarget(target) ? null : 'back'
  }
  return null
}
