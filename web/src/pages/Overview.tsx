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
  const stats: { label: string; value: number | undefined; onClick?: () => void }[] = [
    { label: '密钥组', value: overview?.groups },
    { label: '密钥', value: overview?.secrets, onClick: () => onGo('secrets') },
    { label: '用户', value: overview?.users, onClick: () => onGo('users') },
    { label: 'Provider', value: overview?.providers },
  ]

  return (
    <div className="mx-auto max-w-[900px] px-8 py-8">
      <div className="mono-label mb-2">OVERVIEW</div>

      <div className="grid grid-cols-4 gap-px border border-line bg-line">
        {stats.map((s) => (
          <button
            key={s.label}
            onClick={s.onClick}
            className={s.onClick ? 'group bg-surface p-5 text-left transition-colors hover:bg-surface2' : 'bg-surface p-5 text-left'}
          >
            <div className="mono-label">{s.label}</div>
            <div
              className={`mt-2 font-mono text-[44px] leading-[52px] ${
                s.onClick ? 'text-text group-hover:text-accent' : 'text-text'
              }`}
            >
              {s.value ?? '—'}
            </div>
          </button>
        ))}
      </div>

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
