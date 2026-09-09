import { useEffect, useState } from 'react'
import { api, apiUrl } from '../api'
import { DocumentCard } from './Card'
import type { Go } from '../App'
import { VIS_LABELS } from '../types'
import type {
  LocalPreference,
  RestoreStatus,
  VisibilityPolicyResponse,
  VisibilityPolicySaveResponse,
} from '../types'

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

      <LocalPreferenceCard />

      <BackupRestoreCard />
    </div>
  )
}

/**
 * BackupRestoreCard: download the full-library zip, and restore/migrate via
 * upload. A chosen backup is validated (manifest + SQLite integrity) and
 * STAGED, then applied at the next app start — the live database is never
 * overwritten while open. The previous library is kept as a rollback copy.
 */
function BackupRestoreCard() {
  const [status, setStatus] = useState<RestoreStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')

  const refresh = () =>
    api
      .getRestoreStatus()
      .then(setStatus)
      .catch(() => {}) // 501 on ephemeral builds: card still offers download

  useEffect(() => {
    void refresh()
  }, [])

  const upload = async (file: File) => {
    setBusy(true)
    setError('')
    setNote('')
    try {
      const st = await api.uploadRestore(file)
      setStatus(st)
      setNote('校验通过，恢复包已暂存。请完全退出并重新打开应用，数据将在启动时恢复。')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const cancel = async () => {
    setBusy(true)
    setError('')
    try {
      setStatus(await api.cancelRestore())
      setNote('已取消暂存的恢复包，数据保持不变。')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <DocumentCard title="数据备份与迁移" defaultOpen>
      <p className="mb-3 text-sm text-slate-500">
        一次下载完整库备份（<b>.zip</b>）：数据库一致性快照 + 全部附件文件，应用运行中也可安全导出。
        建议定期复制到网盘/移动硬盘。<b>换机迁移/恢复</b>：在新机器安装并打开一次应用后，
        在下方选择备份 zip 上传，校验通过后<b>完全退出并重新打开应用</b>即可；恢复前的现有数据会自动保留一份
        <code className="rounded bg-slate-100 px-1">restore.rollback-*</code> 回退副本。
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <a className="btn-ghost inline-block" href={apiUrl('/api/v1/backup')} download>
          ⬇ 下载数据备份（.zip）
        </a>
        <label className="btn-ghost inline-block cursor-pointer">
          ⬆ 上传备份并恢复
          <input
            type="file"
            accept=".zip,application/zip"
            className="hidden"
            disabled={busy}
            onChange={(e) => {
              const f = e.target.files?.[0]
              if (f) void upload(f)
              e.target.value = ''
            }}
          />
        </label>
      </div>

      {status?.staged && status.manifest && (
        <div className="mt-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
          <div className="font-medium">
            ⏳ 已暂存恢复包（备份时间 {new Date(status.manifest.created_at).toLocaleString()}，
            附件 {status.manifest.attachment_count} 个）
          </div>
          <div className="mt-1">
            {status.hint ?? '完全退出并重新打开应用后生效。'}
          </div>
          <button className="btn-ghost mt-2 !py-1 text-xs" disabled={busy} onClick={cancel}>
            取消本次恢复
          </button>
        </div>
      )}
      {error && (
        <div className="mt-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
          恢复包校验失败，未改动任何数据：{error}
        </div>
      )}
      {note && !error && !status?.staged && (
        <div className="mt-3 rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
          {note}
        </div>
      )}
    </DocumentCard>
  )
}

/**
 * LocalPreferenceCard configures the home region (本地供应商偏好): once set,
 * suppliers in the region rank first in every list/search. Ranking only —
 * out-of-region suppliers still appear afterward. Stored server-side with
 * the database (travels with backups), honored by HTTP/CLI/MCP alike.
 */
function LocalPreferenceCard() {
  const [pref, setPref] = useState<LocalPreference | null>(null)
  const [province, setProvince] = useState('')
  const [city, setCity] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')

  useEffect(() => {
    api
      .getLocalPreference()
      .then((p) => {
        setPref(p)
        setProvince(p.province ?? '')
        setCity(p.city ?? '')
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  const save = async () => {
    if (!province.trim()) {
      setError('请填写省份（如 浙江）；只需省级偏好时城市可留空')
      return
    }
    setBusy(true)
    setError('')
    setNote('')
    try {
      const p = await api.saveLocalPreference(province.trim(), city.trim())
      setPref(p)
      setNote('已保存：列表与搜索中本地供应商将排在最前（仅排序，不影响筛选）。')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const clear = async () => {
    setBusy(true)
    setError('')
    setNote('')
    try {
      const p = await api.clearLocalPreference()
      setPref(p)
      setProvince('')
      setCity('')
      setNote('已清除本地偏好，列表恢复默认排序。')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <DocumentCard title="本地供应商偏好（本地优先）" defaultOpen>
      <p className="mb-3 text-sm text-slate-500">
        设置常驻地域后，<b>列表与搜索结果中本地供应商自动排到最前</b>，外地供应商仍然显示在后面
        （只调整排序，不会筛选掉任何供应商）。CLI（<code className="rounded bg-slate-100 px-1">srm-cli list/search</code>）
        与 MCP 搜索同样生效；随数据库一起备份。
      </p>
      <div className="flex flex-wrap items-end gap-4">
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          省份（必填）
          <input
            className="w-36 rounded-md border border-slate-300 px-2 py-1.5"
            placeholder="如 浙江"
            value={province}
            disabled={busy}
            onChange={(e) => setProvince(e.target.value)}
          />
        </label>
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          城市（可选）
          <input
            className="w-36 rounded-md border border-slate-300 px-2 py-1.5"
            placeholder="如 杭州"
            value={city}
            disabled={busy}
            onKeyDown={(e) => e.key === 'Enter' && save()}
            onChange={(e) => setCity(e.target.value)}
          />
        </label>
        <button className="btn-primary" disabled={busy || !pref} onClick={save}>
          {busy ? '保存中…' : '保存偏好'}
        </button>
        {pref?.configured && (
          <button className="btn-ghost" disabled={busy} onClick={clear}>
            清除偏好
          </button>
        )}
        <span
          className={`rounded-full px-2.5 py-0.5 text-xs ${
            pref?.configured ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-500'
          }`}
        >
          {pref?.configured ? `已配置：${pref.province}${pref.city ? ` · ${pref.city}` : ''}` : '未配置'}
        </span>
      </div>
      {error && (
        <div className="mt-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>
      )}
      {note && !error && (
        <div className="mt-3 rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
          {note}
        </div>
      )}
    </DocumentCard>
  )
}
