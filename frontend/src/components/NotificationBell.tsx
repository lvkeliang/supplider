import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import type { Go } from '../App'
import { Icon } from './Icon'
import type { SupplierNotification } from '../types'

// NotificationBell is the in-app 变更通知 center (关注供应商的风险/黑名单/
// 归档/合并/资质临期提醒). Pure local feed; the personal-tier realization of
// PRD "变更推送通知关注者" — enterprise later fans the same payload to
// 钉钉/企微. Polls periodically and opens as a dropdown; clicking an item
// acknowledges it and opens that supplier.
export function NotificationBell({ go }: { go: Go }) {
  const [open, setOpen] = useState(false)
  const [unread, setUnread] = useState(0)
  const [items, setItems] = useState<SupplierNotification[]>([])
  const ref = useRef<HTMLDivElement>(null)

  const refresh = useCallback(async () => {
    try {
      const res = await api.notifications(false)
      setUnread(res.unread)
      setItems(res.items)
    } catch {
      // Sidecar momentarily unavailable — the bell stays quiet, never blocks.
    }
  }, [])

  useEffect(() => {
    void refresh()
    const t = setInterval(refresh, 60_000)
    return () => clearInterval(t)
  }, [refresh])

  // Close on outside click.
  useEffect(() => {
    if (!open) return
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [open])

  const openItem = async (n: SupplierNotification) => {
    if (!n.read) {
      try {
        await api.markNotificationRead(n.id)
      } catch {
        /* best-effort */
      }
    }
    setOpen(false)
    if (n.supplier_id) go({ name: 'detail', id: n.supplier_id })
    void refresh()
  }

  const markAll = async () => {
    try {
      await api.markAllNotificationsRead()
    } catch {
      /* best-effort */
    }
    void refresh()
  }

  return (
    <div className="relative" ref={ref}>
      <button
        onClick={() => {
          setOpen((o) => !o)
          void refresh()
        }}
        className="relative rounded-md px-2 py-1 text-sm text-slate-500 hover:bg-slate-100 hover:text-slate-700"
        title="变更通知（关注的供应商）"
        aria-label="变更通知"
      >
        <Icon name="bell" size={18} />
        {unread > 0 && (
          <span className="absolute -right-0.5 -top-0.5 grid min-w-[18px] place-items-center rounded-full bg-red-500 px-1 text-[10px] font-semibold leading-[18px] text-white">
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </button>

      {open && (
        <div className="absolute right-0 z-30 mt-2 w-96 max-w-[92vw] overflow-hidden rounded-lg border border-slate-200 bg-white shadow-lg">
          <div className="flex items-center justify-between border-b border-slate-100 px-3 py-2">
            <span className="text-sm font-semibold text-slate-700">变更通知</span>
            <button
              onClick={markAll}
              disabled={unread === 0}
              className="text-xs text-brand-600 hover:underline disabled:text-slate-300 disabled:no-underline"
            >
              全部标为已读
            </button>
          </div>
          <div className="max-h-[24rem] overflow-y-auto">
            {items.length === 0 ? (
              <p className="px-3 py-6 text-center text-sm text-slate-400">暂无通知</p>
            ) : (
              items.map((n) => (
                <button
                  key={n.id}
                  onClick={() => openItem(n)}
                  className={`flex w-full gap-2 border-b border-slate-50 px-3 py-2 text-left hover:bg-slate-50 ${
                    n.read ? '' : 'bg-blue-50/40'
                  }`}
                >
                  <span className={`mt-0.5 inline-flex shrink-0 ${n.read ? 'text-slate-300' : severityColor(n.severity)}`}>
                    <Icon name={severityIcon(n.severity)} size={15} />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center justify-between gap-2">
                      <span className="truncate text-sm font-medium text-slate-800">{n.title}</span>
                      {!n.read && <span className="h-2 w-2 shrink-0 rounded-full bg-blue-500" />}
                    </span>
                    <span className="block truncate text-xs text-slate-500">
                      {n.supplier_name}
                      {n.body ? ` — ${n.body}` : ''}
                    </span>
                    <span className="block text-[11px] text-slate-400">
                      {formatTime(n.created_at)}
                    </span>
                  </span>
                </button>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function severityIcon(severity: string): 'ban' | 'alert' | 'bell' {
  switch (severity) {
    case 'danger':
      return 'ban'
    case 'warning':
      return 'alert'
    default:
      return 'bell'
  }
}

function severityColor(severity: string): string {
  switch (severity) {
    case 'danger':
      return 'text-red-500'
    case 'warning':
      return 'text-amber-500'
    default:
      return 'text-brand-500'
  }
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}
