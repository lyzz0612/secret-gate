import { useEffect, useState, type ReactNode } from 'react'
import { api } from '../api'
import { CopyBtn } from '../ui'

export function SettingsPage() {
  const [status, setStatus] = useState<{ version: string; schema: number } | null>(null)
  const [overview, setOverview] = useState<{ users: number; pending: number } | null>(null)

  useEffect(() => {
    api.status().then(setStatus).catch(() => {})
    api.overview().then(setOverview).catch(() => {})
  }, [])

  const vacuum = `sqlite3 data/app.db "VACUUM INTO 'data/backup-$(date +%F).db'"`

  return (
    <div className="mx-auto max-w-[760px] px-8 py-6">
      <div className="mono-label mb-4">SETTINGS</div>

      <div className="grid grid-cols-2 gap-px border border-line bg-line">
        <div className="bg-surface p-4">
          <div className="mono-label">版本</div>
          <div className="mt-1 font-mono text-[15px] text-text">v{status?.version ?? '—'}</div>
        </div>
        <div className="bg-surface p-4">
          <div className="mono-label">Schema</div>
          <div className="mt-1 font-mono text-[15px] text-text">v{status?.schema ?? '—'}</div>
        </div>
      </div>

      <Section title="口令与主密钥">
        <Row label="ADMIN_PASSWORD">
          来自 Docker 环境变量。改口令 = 改 compose 配置并重启，所有旧会话自动失效。后台不提供改口令入口。
        </Row>
        <Row label="MASTER_KEY">
          同样来自环境变量，≥ 32 字节。密钥用它派生 AES-256-GCM 加密后入库——
          <span className="text-text">单独拿走 app.db 解不开任何密钥</span>。
          启动时与库内哨兵核对，不匹配直接拒绝启动。
        </Row>
        <Row label="备份">
          手工执行（规划里明确不做定时备份）：
          <div className="mt-2 flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate border border-line bg-bg px-3 py-2 font-mono text-[11.5px] text-text">
              {vacuum}
            </code>
            <CopyBtn getText={() => vacuum} />
          </div>
          <div className="mt-1.5 text-[11px] text-muted">备份 app.db 时必须同时记下当时的 MASTER_KEY，两者分开存。</div>
        </Row>
      </Section>

      <Section title="接入速查">
        <Row label="GitHub Actions">
          workflow 需要 <code className="font-mono text-[11.5px] text-accent">permissions: id-token: write</code>，
          audience 填 Provider 实例里配置的值，出口见 README。
        </Row>
        <Row label="脚本 / 其他 CI">
          建 password 类型实例，用 Basic auth 调 <code className="font-mono text-[11.5px] text-accent">/v1/export</code>。
        </Row>
        <Row label="当前身份">
          {overview ? `${overview.users} 个用户（${overview.pending} 待批准）` : '—'}。首次敲门的身份是 pending，
          批准前读不到任何密钥。
        </Row>
      </Section>
    </div>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="mt-6">
      <div className="mono-label mb-2 border-b border-line pb-1.5">{title}</div>
      <div className="space-y-3">{children}</div>
    </div>
  )
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[130px_1fr] gap-4 border border-line bg-surface p-4">
      <div className="mono-label pt-0.5">{label}</div>
      <div className="text-[12.5px] leading-6 text-muted">{children}</div>
    </div>
  )
}
