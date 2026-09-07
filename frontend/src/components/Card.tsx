import { useState, type ReactNode } from 'react'

/**
 * DocumentCard is one collapsible section on the supplier detail page.
 * The supplier profile is 文档式: each facet (基本信息/资质/绩效/自定义字段…)
 * is an independent card the user can collapse, rather than one rigid fixed
 * form — matching how construction teams actually keep supplier dossiers.
 */
export function DocumentCard({
  title,
  count,
  defaultOpen = true,
  children,
  actions,
}: {
  title: ReactNode
  count?: number
  defaultOpen?: boolean
  children: ReactNode
  actions?: ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <section className="rounded-lg border border-slate-200 bg-white shadow-sm">
      <header className="flex items-center gap-2 px-4 py-3">
        <button
          onClick={() => setOpen((o) => !o)}
          className="flex flex-1 items-center gap-2 text-left"
        >
          <span className="text-slate-400 transition-transform" style={{ transform: open ? 'rotate(90deg)' : 'none' }}>
            ▸
          </span>
          <h2 className="text-sm font-semibold text-slate-700">{title}</h2>
          {count !== undefined && count > 0 && (
            <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500">{count}</span>
          )}
        </button>
        {actions}
      </header>
      {open && <div className="border-t border-slate-100 px-4 py-3">{children}</div>}
    </section>
  )
}

/** A two-column label/value row used inside cards. */
export function Field({ label, value }: { label: string; value?: ReactNode }) {
  if (value === undefined || value === null || value === '') return null
  return (
    <div className="py-1">
      <dt className="text-xs text-slate-400">{label}</dt>
      <dd className="text-sm text-slate-800">{value}</dd>
    </div>
  )
}
