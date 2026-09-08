import { useEffect, useState } from 'react'
import { api } from './api'
import type { Features } from './types'
import { SupplierList } from './components/SupplierList'
import { SupplierForm } from './components/SupplierForm'
import { SupplierDetail } from './components/SupplierDetail'
import { ImportView } from './components/ImportView'
import { SettingsView } from './components/SettingsView'
import { CompareView } from './components/CompareView'

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

export default function App() {
  const [view, setView] = useState<View>({ name: 'list' })
  const [features, setFeatures] = useState<Features | null>(null)
  const [featError, setFeatError] = useState(false)

  useEffect(() => {
    api
      .features()
      .then(setFeatures)
      .catch(() => setFeatError(true))
  }, [])

  const go = (v: View) => setView(v)

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
            {featError && (
              <span className="rounded-full bg-amber-50 px-2.5 py-0.5 text-xs text-amber-700">
                后端未连接
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

export type Go = (v: View) => void
export type { View }
