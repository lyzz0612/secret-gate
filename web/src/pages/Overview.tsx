import type { Overview, Status } from '../types'

export function OverviewPage({
  overview,
  status,
  onGo,
}: {
  overview: Overview | null
  status: Status | null
  onGo: (page: 'users' | 'secrets') => void
}) {
  const stats: { label: string; value: number | undefined; warn?: boolean; onClick?: () => void }[] = [
    { label: '密钥组', value: overview?.groups },
    { label: '密钥', value: overview?.secrets, onClick: () => onGo('secrets') },
    { label: '用户', value: overview?.users },
    { label: '待批准', value: overview?.pending, warn: (overview?.pending ?? 0) > 0, onClick: () => onGo('users') },
    { label: 'Provider', value: overview?.providers },
  ]

  return (
    <div className="mx-auto max-w-[900px] px-8 py-8">
      <div className="mono-label mb-2">OVERVIEW</div>

      <div className="grid grid-cols-5 gap-px border border-line bg-line">
        {stats.map((s) => (
          <button
            key={s.label}
            onClick={s.onClick}
            className={s.onClick ? 'group bg-surface p-5 text-left transition-colors hover:bg-surface2' : 'bg-surface p-5 text-left'}
          >
            <div className="mono-label">{s.label}</div>
            <div
              className={`mt-2 font-mono text-[44px] leading-[52px] ${
                s.warn ? 'text-warn' : s.onClick ? 'text-text group-hover:text-accent' : 'text-text'
              }`}
            >
              {s.value ?? '—'}
            </div>
          </button>
        ))}
      </div>

      {(overview?.pending ?? 0) > 0 && (
        <div className="mt-4 flex items-center gap-3 border-l-2 border-warn bg-warn/5 px-4 py-3">
          <span className="breathe inline-block h-2 w-2 bg-warn" />
          <span className="text-[13px]">
            有 {overview!.pending} 个新身份在敲门，等待批准——批准前它们读不到任何密钥。
          </span>
        </div>
      )}

      <div className="mt-8 grid grid-cols-2 gap-px border border-line bg-line">
        <div className="bg-surface p-4">
          <div className="mono-label">版本</div>
          <div className="mt-1 font-mono text-[15px] text-text">v{status?.version ?? '—'}</div>
        </div>
        <div className="bg-surface p-4">
          <div className="mono-label">Schema</div>
          <div className="mt-1 font-mono text-[15px] text-text">v{status?.schema ?? '—'}</div>
        </div>
      </div>
    </div>
  )
}
