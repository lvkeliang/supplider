import { useEffect, useState, type ReactNode } from 'react'
import { api } from './api'
import type { Features } from './types'
import { SupplierList } from './components/SupplierList'
import { SupplierForm } from './components/SupplierForm'
import { SupplierDetail } from './components/SupplierDetail'
import { ImportView } from './components/ImportView'
import { SettingsView } from './components/SettingsView'
import { CompareView } from './components/CompareView'
import { NotificationBell } from './components/NotificationBell'

// Minimal in-app router (no extra dependency): the desktop MVP has a handful
// of screens. Tauri/web history is not needed for the seed milestone.
type View =
  | { name: 'list' }
  | { name: 'new' }
  | { name: 'detail'; id: string }
  | { name: 'edit'; id: string }
  | { name: 'import' }
  | { name: 'settings' }
  | { name: 'compare'; ids: string[] }

const TIER_LABELS: Record<string, string> = {
  personal: '个人版',
  small_business: '小企业版',
  enterprise: '企业版',
}

// Boot connection gate. The desktop window paints before the Go sidecar has
// finished starting (cold disk / antivirus scan of an unsigned binary can add
// several seconds), so the first request can fail with "Failed to fetch".
// Poll /features until the backend answers, matching the 20s readiness budget
// the Tauri shell uses; only mount the real screens once connected so their
// first fetch succeeds. A later failure is surfaced by each view as before.
const CONNECT_INTERVAL_MS = 500
const CONNECT_MAX_ATTEMPTS = 40 // 40 × 500ms ≈ 20s, mirrors the Rust shell's readiness poll

type Connection = 'connecting' | 'online' | 'offline'

export default function App() {
  const [view, setView] = useState<View>({ name: 'list' })
  const [connection, setConnection] = useState<Connection>('connecting')
  const [features, setFeatures] = useState<Features | null>(null)

  useEffect(() => {
    if (connection !== 'connecting') return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    let tries = 0

    const tick = async () => {
      tries += 1
      try {
        const feats = await api.features()
        if (cancelled) return
        setFeatures(feats)
        setConnection('online')
        return
      } catch {
        // Sidecar still booting (or not started); retry until the budget runs out.
        if (cancelled) return
        if (tries >= CONNECT_MAX_ATTEMPTS) {
          setConnection('offline')
          return
        }
        timer = setTimeout(tick, CONNECT_INTERVAL_MS)
      }
    }
    void tick()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [connection])

  const go = (v: View) => setView(v)

  if (connection === 'connecting') {
    return (
      <BootScreen
        title="正在启动 Supplider 本地服务…"
        subtitle="首次启动可能需要几秒钟，请稍候"
        spinner
      />
    )
  }
  if (connection === 'offline') {
    return (
      <BootScreen
        title="无法连接本地服务"
        subtitle="后端服务没有在 20 秒内就绪。请确认安全软件未拦截 Supplider，然后重试。"
      >
        <button className="btn-primary" onClick={() => setConnection('connecting')}>
          重新连接
        </button>
      </BootScreen>
    )
  }

  return (
    <div className="min-h-screen">
      <header className="border-b border-slate-200 bg-white">
        <div className="mx-auto flex max-w-6xl items-center gap-3 px-4 py-3">
          <button
            onClick={() => go({ name: 'list' })}
            className="flex items-center gap-2 text-lg font-semibold text-slate-800"
          >
            <span className="grid h-8 w-8 place-items-center rounded-md bg-brand-600 text-white">供</span>
            Supplider
          </button>
          <span className="text-sm text-slate-400">供应商资源管理</span>
          <div className="ml-auto flex items-center gap-2">
            {/* 变更通知：关注供应商的风险/黑名单/归档/资质临期提醒。 */}
            <NotificationBell go={go} />
            {/* 管理设置：可见性策略等管理员配置（个人版单用户即管理员）。 */}
            <button
              onClick={() => go({ name: 'settings' })}
              className="rounded-md px-2 py-1 text-sm text-slate-500 hover:bg-slate-100 hover:text-slate-700"
              title="管理设置（可见性策略）"
            >
              ⚙ 设置
            </button>
            {features && (
              <span className="rounded-full bg-slate-100 px-2.5 py-0.5 text-xs text-slate-600">
                {TIER_LABELS[features.tier] ?? features.tier}
              </span>
            )}
            {/* AI 入口只在后端报告 ai_enabled 时出现（无 Key 时整个隐藏，平台 100% 可用）。 */}
            {features?.ai_enabled && (
              <span className="rounded-full bg-emerald-50 px-2.5 py-0.5 text-xs text-emerald-700">
                AI 增强已启用
              </span>
            )}
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-4 py-6">
        {view.name === 'list' && <SupplierList go={go} />}
        {view.name === 'new' && (
          <SupplierForm
            go={go}
            visibilityLevels={features?.visibility_levels ?? 2}
          />
        )}
        {view.name === 'detail' && <SupplierDetail id={view.id} go={go} />}
        {view.name === 'edit' && (
          <SupplierForm
            go={go}
            id={view.id}
            visibilityLevels={features?.visibility_levels ?? 2}
          />
        )}
        {view.name === 'import' && (
          <ImportView go={go} visibilityLevels={features?.visibility_levels ?? 2} />
        )}
        {view.name === 'settings' && <SettingsView go={go} />}
        {view.name === 'compare' && <CompareView ids={view.ids} go={go} />}
      </main>
    </div>
  )
}

// BootScreen is the centered state shown while the local backend starts and
// when it cannot be reached (with a retry action slotted in as children).
function BootScreen({
  title,
  subtitle,
  spinner = false,
  children,
}: {
  title: string
  subtitle?: string
  spinner?: boolean
  children?: ReactNode
}) {
  return (
    <div className="grid min-h-screen place-items-center px-4">
      <div className="w-full max-w-sm text-center">
        <div className="mx-auto mb-4 grid h-12 w-12 place-items-center rounded-lg bg-brand-600 text-xl font-semibold text-white">
          供
        </div>
        {spinner && (
          <div className="mx-auto mb-4 h-6 w-6 animate-spin rounded-full border-2 border-slate-300 border-t-brand-600" />
        )}
        <h1 className="text-base font-semibold text-slate-800">{title}</h1>
        {subtitle && <p className="mt-1.5 text-sm text-slate-500">{subtitle}</p>}
        {children && <div className="mt-5 flex justify-center">{children}</div>}
      </div>
    </div>
  )
}

export type Go = (v: View) => void
export type { View }
