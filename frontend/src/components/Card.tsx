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
          aria-expanded={open}
        >
          <span
            className="text-slate-400 transition-transform duration-200"
            style={{ transform: open ? 'rotate(90deg)' : 'none' }}
          >
            ▸
          </span>
          <h2 className="text-sm font-semibold text-slate-700">{title}</h2>
          {count !== undefined && count > 0 && (
            <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500">{count}</span>
          )}
        </button>
        {actions}
      </header>
      {/* TR-14: smooth height transition via the grid 0fr↔1fr technique
          (works for any content height without measuring). Content stays
          mounted; the inner div clips while collapsed. */}
      <div
        className={`card-collapse border-t ${open ? 'open border-slate-100' : 'border-transparent'}`}
        // Collapsed content is visually clipped; remove it from the a11y
        // tree and tab order (React 18 lacks the inert prop — set via ref).
        ref={(el) => {
          if (el) el.inert = !open
        }}
      >
        <div>
          <div className="px-4 py-3">{children}</div>
        </div>
      </div>
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
