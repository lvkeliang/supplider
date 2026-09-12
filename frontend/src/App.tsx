import { useEffect, useState, type ReactNode } from 'react'
import { api, setNetworkEventListener, type NetworkEvent } from './api'
import { FOCUS_SEARCH_EVENT, hotkeyAction } from './hotkeys'
import type { Features } from './types'
import { SupplierList } from './components/SupplierList'
import { SupplierForm } from './components/SupplierForm'
import { SupplierDetail } from './components/SupplierDetail'
import { ImportView } from './components/ImportView'
import { SettingsView } from './components/SettingsView'
import { CompareView } from './components/CompareView'
import { NotificationBell } from './components/NotificationBell'
import { Logo } from './components/Logo'
import { Icon } from './components/Icon'

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

// Connection gate states:
//   connecting - cold start: poll /features up to the 20s shell budget, then
//                fall back to the manual offline screen
//   online     - business screens mounted; a 5s /readyz heartbeat plus
//                passive fetch-failure reports watch the sidecar
//   down       - sidecar vanished mid-session (crash/kill): business screens
//                unmount and we retry FOREVER — re-launching the app spawns a
//                fresh sidecar on the freed port (see sidecar 单实例交接),
//                and the still-open window heals as soon as it answers
//   offline    - cold start never succeeded; manual 重新连接 only
//
// On every entry to online a gateKey bump remounts the business tree, so a
// recovered session refetches everything instead of showing stale errors.
const CONNECT_INTERVAL_MS = 500
const CONNECT_MAX_ATTEMPTS = 40 // 40 × 500ms ≈ 20s, mirrors the Rust shell's readiness poll
const RECONNECT_INTERVAL_MS = 1000
const HEARTBEAT_MS = 5000
const HEARTBEAT_MISSES_TO_FLIP = 2

type Connection = 'connecting' | 'online' | 'down' | 'offline'

export default function App() {
  const [view, setView] = useState<View>({ name: 'list' })
  const [connection, setConnection] = useState<Connection>('connecting')
  const [features, setFeatures] = useState<Features | null>(null)
  const [gateKey, setGateKey] = useState(0)
  const [retryNonce, setRetryNonce] = useState(0)

  // become-online poller: the bounded cold-start gate and the unbounded
  // mid-session reconnect loop share one implementation.
  useEffect(() => {
    if (connection !== 'connecting' && connection !== 'down') return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    let tries = 0
    const interval = connection === 'connecting' ? CONNECT_INTERVAL_MS : RECONNECT_INTERVAL_MS

    const tick = async () => {
      tries += 1
      try {
        const feats = await api.features()
        if (cancelled) return
        setFeatures(feats)
        // Remount every business view so its first request after recovery
        // is a fresh fetch (the old subtree died in an error state).
        setGateKey((k) => k + 1)
        setConnection('online')
        return
      } catch {
        // Sidecar still booting / still gone; retry. Cold start eventually
        // gives up to the manual screen; a mid-session loss retries forever
        // (recovery may arrive via a second app launch at any time).
        if (cancelled) return
        if (connection === 'connecting' && tries >= CONNECT_MAX_ATTEMPTS) {
          setConnection('offline')
          return
        }
        timer = setTimeout(tick, interval)
      }
    }
    void tick()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [connection, retryNonce])

  // online watchdog: 5s /readyz heartbeat plus immediate confirmation of any
  // rejected fetch (user action failed). HTTP 4xx/5xx count as success —
  // only a rejected fetch means the sidecar port is gone.
  useEffect(() => {
    if (connection !== 'online') return
    let misses = 0
    let probing = false

    const confirmDown = async () => {
      if (probing) return
      probing = true
      try {
        await api.readyz()
        misses = 0 // one-off abort/blip: the backend actually answered
      } catch {
        setConnection('down')
      } finally {
        probing = false
      }
    }
    const listener = (event: NetworkEvent) => {
      if (event === 'failure') {
        void confirmDown()
        return
      }
      misses = 0
    }
    setNetworkEventListener(listener)
    const heartbeat = setInterval(() => {
      api
        .readyz()
        .then(() => {
          misses = 0
        })
        .catch(() => {
          misses += 1
          if (misses >= HEARTBEAT_MISSES_TO_FLIP) setConnection('down')
        })
    }, HEARTBEAT_MS)
    return () => {
      setNetworkEventListener(null)
      clearInterval(heartbeat)
    }
  }, [connection])

  const go = (v: View) => setView(v)

  // Global shortcuts (TR-18). Esc returns to the list only from another view.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      switch (hotkeyAction(e, e.target)) {
        case 'new':
          e.preventDefault()
          go({ name: 'new' })
          break
        case 'search':
          e.preventDefault()
          window.dispatchEvent(new CustomEvent(FOCUS_SEARCH_EVENT))
          break
        case 'back':
          if (view.name !== 'list') go({ name: 'list' })
          break
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view.name])

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
  if (connection === 'down') {
    return (
      <BootScreen
        title="本地服务连接中断"
        subtitle="正在自动尝试重新连接，数据保存在本地不会丢失。若长时间未恢复，请完全退出 Supplider 后重新双击启动。"
        spinner
      >
        <button className="btn-primary" onClick={() => setRetryNonce((n) => n + 1)}>
          立即重试
        </button>
      </BootScreen>
    )
  }

  return (
    // gateKey remounts the whole business tree when a lost session returns,
    // forcing every view (and the bell) to refetch from scratch.
    <div className="min-h-screen" key={gateKey}>
      <header className="sticky top-0 z-50 border-b border-slate-200 bg-white">
        <div className="mx-auto flex max-w-6xl items-center gap-3 px-4 py-3">
          <button
            onClick={() => go({ name: 'list' })}
            className="flex items-center gap-2 text-lg font-semibold text-slate-800"
          >
            <Logo size={32} />
            Supplider
          </button>
          <span className="text-sm text-slate-400">供应商资源管理</span>
          <div className="ml-auto flex items-center gap-2">
            {/* 变更通知：关注供应商的风险/黑名单/归档/资质临期提醒。 */}
            <NotificationBell go={go} />
            {/* 管理设置：可见性策略等管理员配置（个人版单用户即管理员）。 */}
            <button
              onClick={() => go({ name: 'settings' })}
              className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-sm text-slate-500 hover:bg-slate-100 hover:text-slate-700"
              title="管理设置（可见性策略）"
            >
              <Icon name="settings" size={16} /> 设置
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
            aiOCR={features?.ai_ocr_entry ?? false}
          />
        )}
        {view.name === 'detail' && <SupplierDetail id={view.id} go={go} />}
        {view.name === 'edit' && (
          <SupplierForm
            go={go}
            id={view.id}
            visibilityLevels={features?.visibility_levels ?? 2}
            aiOCR={features?.ai_ocr_entry ?? false}
          />
        )}
        {view.name === 'import' && (
          <ImportView
            go={go}
            visibilityLevels={features?.visibility_levels ?? 2}
            aiExcelMapping={features?.ai_excel_mapping ?? false}
          />
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
        <Logo size={48} className="mx-auto mb-4" />
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
