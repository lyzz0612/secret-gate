import { useEffect, useState } from 'react'
import { api } from './api'
import { Btn, cn } from './ui'
import type { Overview, Status } from './types'
import { Login } from './pages/Login'
import { OverviewPage } from './pages/Overview'
import { SecretsPage } from './pages/Secrets'
import { ProvidersPage } from './pages/Providers'
import { UsersPage } from './pages/Users'
import { UserGroupsPage } from './pages/UserGroups'
import { GrantsPage } from './pages/Grants'
import { SettingsPage } from './pages/Settings'

type Page = 'overview' | 'secrets' | 'grants' | 'users' | 'usergroups' | 'providers' | 'settings'

const NAV: { group: string; items: { page: Page; label: string }[] }[] = [
  { group: '', items: [{ page: 'overview', label: '总览' }] },
  { group: '密钥管理', items: [{ page: 'secrets', label: '密钥' }] },
  {
    group: '访问控制',
    items: [
      { page: 'grants', label: '授权' },
      { page: 'users', label: '用户' },
      { page: 'usergroups', label: '用户组' },
    ],
  },
  { group: '接入', items: [{ page: 'providers', label: 'Provider' }] },
  { group: '系统', items: [{ page: 'settings', label: '设置' }] },
]

export default function App() {
  const [phase, setPhase] = useState<'loading' | 'login' | 'app'>('loading')
  const [page, setPage] = useState<Page>('overview')
  const [overview, setOverview] = useState<Overview | null>(null)
  const [status, setStatus] = useState<Status | null>(null)

  const refreshOverview = () => {
    api.overview().then(setOverview).catch(() => {})
    api.status().then(setStatus).catch(() => {})
  }

  useEffect(() => {
    api
      .me()
      .then(() => setPhase('app'))
      .catch(() => setPhase('login'))
  }, [])

  useEffect(() => {
    if (phase === 'app') refreshOverview()
  }, [phase])

  if (phase === 'loading') {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="mono-label breathe">SECRET GATE</div>
      </div>
    )
  }
  if (phase === 'login') return <Login onSuccess={() => setPhase('app')} />

  return (
    <div className="flex h-full">
      {/* 侧栏 */}
      <aside className="flex w-[220px] shrink-0 flex-col border-r border-line bg-surface">
        <div className="flex items-center gap-2 px-4 pt-5 pb-4">
          <span className="inline-block h-3.5 w-3.5 bg-accent" />
          <span className="font-mono text-[12px] font-medium uppercase tracking-[0.18em] text-text">
            Secret&nbsp;Gate
          </span>
        </div>
        <nav className="flex-1 overflow-y-auto px-2 pb-4">
          {NAV.map((sec) => (
            <div key={sec.group} className="mb-4">
              {sec.group && <div className="mono-label px-2 pb-1.5 pt-2">{sec.group}</div>}
              {sec.items.map((item) => {
                const active = page === item.page
                const showPending = item.page === 'users' && (overview?.pending ?? 0) > 0
                return (
                  <button
                    key={item.page}
                    onClick={() => setPage(item.page)}
                    className={cn(
                      'relative flex w-full items-center gap-2 px-2 py-[7px] text-left text-[13px] transition-colors',
                      active ? 'bg-surface2 text-accent' : 'text-muted hover:bg-surface2/60 hover:text-text',
                    )}
                  >
                    {showPending && <span className="breathe absolute left-0 top-1 bottom-1 w-[2px] bg-warn" />}
                    <span className={cn('ml-0.5 inline-block h-1 w-1', active ? 'bg-accent' : 'bg-line2')} />
                    {item.label}
                    {showPending && (
                      <span className="ml-auto font-mono text-[11px] text-warn">{overview!.pending}</span>
                    )}
                  </button>
                )
              })}
            </div>
          ))}
        </nav>
        <div className="border-t border-line px-4 py-3">
          <div className="flex items-center gap-2">
            <span className="inline-block h-1.5 w-1.5 rounded-full bg-accent" />
            <span className="mono-label">运行中</span>
            <span className="mono-label ml-auto text-line2">v{status?.version ?? '—'}</span>
          </div>
        </div>
      </aside>

      {/* 主区 */}
      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line px-6">
          <span className="font-mono text-[11px] text-muted">
            {NAV.flatMap((s) => s.items).find((i) => i.page === page)?.label}
          </span>
          <span className="text-line2">/</span>
          <span className="text-[13px]">{subtitle(page, overview)}</span>
          <div className="ml-auto flex items-center gap-2">
            <Btn variant="ghost" className="h-7" onClick={() => setPage('settings')}>
              设置
            </Btn>
            <Btn
              variant="ghost"
              className="h-7"
              onClick={async () => {
                try {
                  await api.logout()
                } catch {
                  /* 忽略 */
                }
                setPhase('login')
              }}
            >
              退出
            </Btn>
          </div>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {page === 'overview' && <OverviewPage overview={overview} status={status} onGo={setPage} />}
          {page === 'secrets' && <SecretsPage onChanged={refreshOverview} />}
          {page === 'grants' && <GrantsPage onChanged={refreshOverview} />}
          {page === 'users' && <UsersPage onChanged={refreshOverview} />}
          {page === 'usergroups' && <UserGroupsPage onChanged={refreshOverview} />}
          {page === 'providers' && <ProvidersPage onChanged={refreshOverview} />}
          {page === 'settings' && <SettingsPage />}
        </div>
      </main>
    </div>
  )
}

function subtitle(page: Page, overview: Overview | null) {
  if (page === 'overview') return overview ? `${overview.groups} 组 · ${overview.secrets} 密钥` : ''
  return ''
}
