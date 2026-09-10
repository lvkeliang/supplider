import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { createPortal } from 'react-dom'

/**
 * Global, zero-dependency toast/notification infrastructure (TR-01).
 *
 * Before this existed the app had no feedback surface at all: successful
 * create/edit/blacklist/archive actions either navigated away or silently
 * reloaded, and the user could not tell whether a click worked. `ToastProvider`
 * mounts a portal viewport once at the app root (main.tsx, OUTSIDE App's
 * gateKey-remounted subtree, so a toast survives view navigation and the
 * down→online reconnect remount); any component calls `useToast()` to push a
 * success/error/info message that auto-dismisses.
 */

type ToastKind = 'success' | 'error' | 'info'

interface ToastItem {
  id: number
  kind: ToastKind
  message: string
}

interface ToastApi {
  push: (message: string, kind?: ToastKind) => void
  success: (message: string) => void
  error: (message: string) => void
  info: (message: string) => void
}

const ToastContext = createContext<ToastApi | null>(null)

// Errors stay longer (the user may need to read why an action failed).
const DISMISS_MS: Record<ToastKind, number> = {
  success: 3500,
  info: 3500,
  error: 6000,
}
// Never let an action storm pile toasts off-screen.
const MAX_VISIBLE = 4

let nextId = 1

/** useToast returns the imperative push API. Must be used under ToastProvider. */
export function useToast(): ToastApi {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used within <ToastProvider>')
  return ctx
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([])

  const dismiss = useCallback((id: number) => {
    setToasts((list) => list.filter((t) => t.id !== id))
  }, [])

  const push = useCallback((message: string, kind: ToastKind = 'info') => {
    const id = nextId++
    setToasts((list) => [...list, { id, kind, message }].slice(-MAX_VISIBLE))
  }, [])

  const api = useMemo<ToastApi>(
    () => ({
      push,
      success: (m) => push(m, 'success'),
      error: (m) => push(m, 'error'),
      info: (m) => push(m, 'info'),
    }),
    [push],
  )

  return (
    <ToastContext.Provider value={api}>
      {children}
      {createPortal(
        <div
          className="pointer-events-none fixed inset-x-0 top-3 z-[100] flex flex-col items-center gap-2 px-4"
          aria-live="polite"
          aria-atomic="true"
        >
          {toasts.map((t) => (
            <ToastBubble key={t.id} toast={t} onClose={() => dismiss(t.id)} />
          ))}
        </div>,
        document.body,
      )}
    </ToastContext.Provider>
  )
}

const KIND_STYLE: Record<ToastKind, { bar: string; icon: string; cls: string }> = {
  success: {
    bar: 'bg-emerald-500',
    icon: '✓',
    cls: 'border-emerald-200 bg-white text-emerald-800',
  },
  error: {
    bar: 'bg-red-500',
    icon: '✕',
    cls: 'border-red-200 bg-white text-red-800',
  },
  info: {
    bar: 'bg-brand-500',
    icon: 'ℹ',
    cls: 'border-slate-200 bg-white text-slate-700',
  },
}

function ToastBubble({ toast, onClose }: { toast: ToastItem; onClose: () => void }) {
  useEffect(() => {
    const timer = setTimeout(onClose, DISMISS_MS[toast.kind])
    return () => clearTimeout(timer)
  }, [toast.kind, onClose])

  const s = KIND_STYLE[toast.kind]
  return (
    <div
      className={`toast-in pointer-events-auto flex w-full max-w-md items-start gap-2.5 overflow-hidden rounded-lg border py-2.5 pl-0 pr-3 text-sm shadow-lg ${s.cls}`}
      role="status"
    >
      <span className={`ml-3 mt-0.5 grid h-5 w-5 shrink-0 place-items-center rounded-full text-xs text-white ${s.bar}`}>
        {s.icon}
      </span>
      <span className="flex-1 leading-5">{toast.message}</span>
      <button
        type="button"
        onClick={onClose}
        className="shrink-0 text-slate-400 hover:text-slate-600"
        aria-label="关闭"
      >
        ✕
      </button>
    </div>
  )
}
