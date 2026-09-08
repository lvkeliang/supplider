import { useEffect, useState } from 'react'
import { api } from '../api'
import { DocumentCard } from './Card'
import type { Go } from '../App'
import { VIS_LABELS } from '../types'
import type { VisibilityPolicyResponse, VisibilityPolicySaveResponse } from '../types'

// SettingsView is the admin console seed surface. For the personal MVP it
// holds one policy: the visibility cap (可见性策略收紧). Tightening it runs
// the disposition flow (扫描 → 通知 → 7 天缓冲 → 超时自动降级 → 申诉);
// the sidecar repeats the sweep automatically at boot and every 24h, so
// saving here is all an administrator has to do.
export function SettingsView({ go }: { go: Go }) {
  const [policy, setPolicy] = useState<VisibilityPolicyResponse | null>(null)
  const [maxLevel, setMaxLevel] = useState(0)
  const [bufferDays, setBufferDays] = useState(7)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState<VisibilityPolicySaveResponse | null>(null)

  useEffect(() => {
    api
      .getVisibilityPolicy()
      .then((p) => {
        setPolicy(p)
        setMaxLevel(p.policy.max_level)
        setBufferDays(p.policy.buffer_days)
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  const save = async () => {
    setBusy(true)
    setError('')
    setSaved(null)
    try {
      const res = await api.saveVisibilityPolicy(maxLevel, bufferDays)
      setSaved(res)
      setPolicy({ policy: res.policy, configured: true, tier_max_level: policy?.tier_max_level ?? maxLevel })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const tierMax = policy?.tier_max_level ?? 1

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <button className="btn-ghost px-2 py-1 text-sm" onClick={() => go({ name: 'list' })}>
          ← 返回列表
        </button>
        <h2 className="text-lg font-semibold text-slate-800">管理设置</h2>
      </div>

      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
          {error}
        </div>
      )}

      <DocumentCard title="可见性策略（最高可见等级）" defaultOpen>
        <p className="mb-3 text-sm text-slate-500">
          收紧最高可见等级后，超出范围的供应商记录进入数据处置流程：
          <b>扫描不合规 → 通知录入者 → 缓冲期（默认 7 天）内可自行调整或申诉 → 超时自动降级到合规等级</b>。
          桌面端每次启动与运行中每 24 小时自动执行处置，无需人工操作。
        </p>

        <div className="flex flex-wrap items-end gap-4">
          <label className="flex flex-col gap-1 text-sm text-slate-700">
            最高允许可见等级
            <select
              className="rounded-md border border-slate-300 px-2 py-1.5"
              value={maxLevel}
              disabled={busy || !policy}
              onChange={(e) => setMaxLevel(Number(e.target.value))}
            >
              {Array.from({ length: tierMax + 1 }, (_, i) => (
                <option key={i} value={i}>
                  L{i}　{VIS_LABELS[i] ?? ''}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-sm text-slate-700">
            缓冲期（天）
            <input
              type="number"
              min={1}
              max={90}
              className="w-24 rounded-md border border-slate-300 px-2 py-1.5"
              value={bufferDays}
              disabled={busy || !policy}
              onChange={(e) => setBufferDays(Math.max(1, Number(e.target.value) || 7))}
            />
          </label>
          <button className="btn-primary" disabled={busy || !policy} onClick={save}>
            {busy ? '保存中…' : '保存并立即处置'}
          </button>
          <span
            className={`rounded-full px-2.5 py-0.5 text-xs ${
              policy?.configured
                ? 'bg-emerald-50 text-emerald-700'
                : 'bg-slate-100 text-slate-500'
            }`}
          >
            {policy?.configured ? '管理员策略已配置' : '当前为版本默认策略'}
          </span>
        </div>

        {saved && (
          <div className="mt-4 rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
            策略已保存（最高 L{saved.policy.max_level}，缓冲 {saved.policy.buffer_days} 天），已立即处置一次：
            新标记待调整 <b>{saved.report.flagged}</b>、缓冲期中 {saved.report.pending}、
            申诉中 {saved.report.appealed}、超时自动降级 <b>{saved.report.downgraded}</b>、
            自行调整解除 {saved.report.resolved}。
            {saved.report.flagged > 0 && (
              <> 相关供应商列表与详情页将显示「待调整」徽标，录入者可自行调整或申诉。</>
            )}
          </div>
        )}
      </DocumentCard>
    </div>
  )
}
