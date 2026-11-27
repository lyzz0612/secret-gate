import { useEffect, useState, type ReactNode } from 'react'
import { api, ApiError } from '../api'
import { Btn, CopyBtn } from '../ui'

export function SettingsPage() {
  const [status, setStatus] = useState<{ version: string; schema: number } | null>(null)
  const [ttl, setTTL] = useState<number | null>(null)
  const [ttlDraft, setTtlDraft] = useState('')
  const [ttlMsg, setTtlMsg] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.status().then(setStatus).catch(() => {})
    api
      .settings()
      .then((s) => {
        setTTL(s.token_ttl_minutes)
        setTtlDraft(String(s.token_ttl_minutes))
      })
      .catch(() => {})
  }, [])

  const saveTTL = async () => {
    setBusy(true)
    setTtlMsg('')
    try {
      const n = Number(ttlDraft)
      await api.putSettings(n)
      setTTL(n)
      setTtlMsg('已保存，对新签发的 token 立即生效')
    } catch (e) {
      setTtlMsg(e instanceof ApiError ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const vacuum = `sqlite3 data/app.db "VACUUM INTO 'data/backup-$(date +%F).db'"`

  const authExample = `# 1) 换票（AppKey 用户；账号密码用户改用 -u user:pass）
curl -s -X POST https://secret-gate.example/v1/auth \\
  -H "X-App-Key: ak_xxxxxxxx..." 

# 2) 导出（拿上一步返回的 token；默认 2 分钟内有效）
curl -s https://secret-gate.example/v1/export \\
  -H "Authorization: Bearer <token>"`

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

      <Section title="CI 取密钥（两跳换票）">
        <Row label="token 寿命">
          <div className="flex items-center gap-2">
            <input
              className="h-9 w-24 border border-line bg-bg px-3 font-mono text-[13px] text-text focus:border-accent/70 focus:outline-none"
              value={ttlDraft}
              onChange={(e) => setTtlDraft(e.target.value)}
            />
            <span className="text-[12px] text-muted">分钟（1–1440）</span>
            <Btn variant="primary" className="h-9" disabled={busy || ttlDraft === String(ttl ?? '')} onClick={() => void saveTTL()}>
              {busy ? '保存中…' : '保存'}
            </Btn>
          </div>
          <div className="mt-1.5 text-[11px] text-muted">
            当前 {ttl ?? '—'} 分钟（默认 2）。换票后紧挨着导出即可；改寿命只影响新签发的 token。
            {ttlMsg && <span className="ml-1 text-accent">{ttlMsg}</span>}
          </div>
        </Row>
        <Row label="调用示例">
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 overflow-auto border border-line bg-bg px-3 py-2 font-mono text-[11px] leading-5 whitespace-pre text-text">
              {authExample}
            </code>
            <CopyBtn getText={() => authExample} />
          </div>
          <div className="mt-1.5 text-[11px] text-muted">
            guest 级别用户在第二跳拿到 key 名与固定假值（JSON 带 <code className="font-mono">masked: true</code>），normal 拿真实值。
          </div>
        </Row>
      </Section>

      <Section title="口令与主密钥">
        <Row label="ADMIN_PASSWORD">
          来自 Docker 环境变量。改口令 = 改 compose 配置并重启，所有旧会话自动失效。后台不提供改口令入口。
        </Row>
        <Row label="MASTER_KEY">
          同样来自环境变量，≥ 32 字节。密钥与用户凭证（appkey / 口令）用它派生 AES-256-GCM 加密后入库——
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
        <Row label="OIDC（三类）">
          GitHub Actions / GitLab CI / Vercel 各建一条实例，CI 侧带接入标识换票：
          <code className="ml-1 font-mono text-[11.5px] text-accent">POST /v1/auth/&#123;实例名&#125;</code>。
          GitLab 在 <code className="font-mono text-[11.5px]">id_tokens:</code> 里声明 aud，Vercel 用{' '}
          <code className="font-mono text-[11.5px]">VERCEL_OIDC_TOKEN</code>；不带实例名也能换票（按类型与规则择优）。
        </Row>
        <Row label="脚本 / 其他 CI">
          在「用户」页建 <span className="text-text">appkey</span> 或 <span className="text-text">账号密码</span> 用户，
          配授权并按需提级 normal，然后两跳：<code className="font-mono text-[11.5px] text-accent">POST /v1/auth</code> 换票 →
          <code className="font-mono text-[11.5px] text-accent"> GET /v1/export</code> 导出。
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
