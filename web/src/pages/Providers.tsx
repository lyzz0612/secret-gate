import { useEffect, useMemo, useState } from 'react'
import { api, ApiError } from '../api'
import type { PluginType, Provider, Rule } from '../types'
import { Btn, Confirm, Drawer, ErrorText, Field, TextArea, TextInput, cn } from '../ui'

interface RuleResult {
  field: string
  pattern: string
  hit: boolean
  missing: boolean
}

interface TestOut {
  allowed: boolean
  results: RuleResult[]
}

export function ProvidersPage({ onChanged }: { onChanged: () => void }) {
  const [types, setTypes] = useState<PluginType[]>([])
  const [providers, setProviders] = useState<Provider[]>([])
  const [err, setErr] = useState('')
  const [editDrawer, setEditDrawer] = useState<Provider | 'new' | null>(null)
  const [delTarget, setDelTarget] = useState<Provider | null>(null)
  const [testTarget, setTestTarget] = useState<Provider | null>(null)
  const [busy, setBusy] = useState(false)

  const load = async () => {
    try {
      const [ts, ps] = await Promise.all([api.plugins(), api.providers()])
      setTypes(ts)
      setProviders(ps)
      setErr('')
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '加载失败')
    }
  }
  useEffect(() => {
    void load()
  }, [])

  // 新建实例只可选实例型；内置类型（appkey/password）的凭证在用户行上，去「用户」页建
  const instanceTypes = useMemo(() => types.filter((t) => !t.builtin), [types])

  const parseRules = (p: Provider): Rule[] => {
    try {
      return JSON.parse(p.rules_json) as Rule[]
    } catch {
      return []
    }
  }

  return (
    <div className="mx-auto max-w-[1000px] px-8 py-6">
      <div className="mb-4 flex items-center">
        <div>
          <div className="mono-label">PROVIDERS</div>
          <div className="mt-1 text-[13px] text-muted">
            一个实例 = 一份配置 + 一套准入规则；同类型多个实例按顺序择优
          </div>
        </div>
        <Btn variant="primary" className="ml-auto" onClick={() => setEditDrawer('new')}>
          + 新建实例
        </Btn>
      </div>

      <ErrorText>{err}</ErrorText>

      {providers.length === 0 && (
        <div className="border border-line bg-surface px-6 py-10 text-center">
          <div className="mono-label">EMPTY</div>
          <div className="mt-1 text-[12px] text-muted">还没有实例——建一个 GitHub OIDC 实例开始接入</div>
        </div>
      )}

      <div className="space-y-2">
        {providers.map((p, i) => {
          const rules = parseRules(p)
          return (
            <div
              key={p.id}
              className="row-in border border-line bg-surface p-4 hover:border-line2"
              style={{ animationDelay: `${i * 30}ms` }}
            >
              <div className="flex items-center gap-3">
                <span className={cn('h-2 w-2', p.enabled ? 'bg-accent' : 'bg-line2')} />
                <span className="text-[14px]">{p.name}</span>
                <span className="mono-label border border-line px-1.5 py-0.5">{p.type}</span>
                {!p.enabled && <span className="mono-label text-warn">已停用</span>}
                <div className="ml-auto flex gap-1">
                  <Btn variant="ghost" className="h-7" onClick={() => setTestTarget(p)}>
                    试一下
                  </Btn>
                  <Btn variant="ghost" className="h-7" onClick={() => setEditDrawer(p)}>
                    编辑
                  </Btn>
                  <Btn variant="danger" className="h-7" onClick={() => setDelTarget(p)}>
                    删除
                  </Btn>
                </div>
              </div>
              <div className="mt-3 font-mono text-[12px] text-muted">
                {rules.length > 0 ? (
                  <>
                    <span className="text-text">准入 ×{rules.length}</span>
                    {rules.map((r) => ` · ${r.field}`).join('')}
                  </>
                ) : (
                  '（无规则 = 不放行）'
                )}
              </div>
            </div>
          )
        })}
      </div>

      {/* 新建 / 编辑 */}
      {editDrawer && (
        <ProviderDrawer
          state={editDrawer}
          types={instanceTypes}
          onClose={() => setEditDrawer(null)}
          onSaved={async () => {
            await load()
            onChanged()
          }}
        />
      )}

      {/* 删除 */}
      <Confirm
        open={delTarget != null}
        onOpenChange={(v) => !v && setDelTarget(null)}
        busy={busy}
        title="DELETE PROVIDER"
        onConfirm={async () => {
          if (!delTarget) return
          setBusy(true)
          try {
            await api.deleteProvider(delTarget.id)
            setDelTarget(null)
            await load()
            onChanged()
          } catch (e) {
            setErr(e instanceof ApiError ? e.message : '删除失败')
          } finally {
            setBusy(false)
          }
        }}
      >
        确定删除实例 <span className="font-mono text-warn">{delTarget?.name}</span>？
        用户身份与实例无关，不会被删除——只是少了一个入口。
      </Confirm>

      {/* 试一下 */}
      {testTarget && <TestDrawer provider={testTarget} onClose={() => setTestTarget(null)} />}
    </div>
  )
}

/* ---------- 新建 / 编辑抽屉 ---------- */

function ProviderDrawer({
  state,
  types,
  onClose,
  onSaved,
}: {
  state: Provider | 'new'
  types: PluginType[]
  onClose: () => void
  onSaved: () => Promise<void> | void
}) {
  const isNew = state === 'new'
  const existing = isNew ? null : state
  const [type, setType] = useState(existing?.type ?? types[0]?.type ?? 'github_oidc')
  const [name, setName] = useState(existing?.name ?? '')
  const [enabled, setEnabled] = useState(existing?.enabled ?? true)
  const [config, setConfig] = useState<Record<string, string>>(() => {
    if (!existing) return {}
    try {
      return JSON.parse(existing.config_json)
    } catch {
      return {}
    }
  })
  const [rules, setRules] = useState<Rule[]>(() => {
    if (!existing) return [{ field: 'repository_owner', pattern: '' }]
    try {
      const r = JSON.parse(existing.rules_json) as Rule[]
      return r.length ? r : [{ field: 'repository_owner', pattern: '' }]
    } catch {
      return [{ field: 'repository_owner', pattern: '' }]
    }
  })
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const curType = useMemo(() => types.find((t) => t.type === type), [types, type])
  const claimFields = curType?.claim_fields ?? []

  const submit = async () => {
    setBusy(true)
    setErr('')
    try {
      if (isNew) {
        await api.createProvider(type, name.trim(), config, rules.filter((r) => r.field && r.pattern))
      } else {
        await api.updateProvider(existing!.id, name.trim(), enabled, config, rules.filter((r) => r.field && r.pattern))
      }
      onClose()
      await onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Drawer open onOpenChange={(v) => !v && onClose()} title={isNew ? 'NEW PROVIDER' : 'EDIT PROVIDER'} width={560}>
      <div className="space-y-4">
        <Field label="类型">
          <select
            className="h-9 w-full border border-line bg-bg px-3 text-[13px] text-text focus:border-accent/70 focus:outline-none"
            value={type}
            disabled={!isNew}
            onChange={(e) => setType(e.target.value)}
          >
            {types.map((t) => (
              <option key={t.type} value={t.type}>
                {t.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="实例名">
          <TextInput value={name} placeholder="我的个人号" onChange={(e) => setName(e.target.value)} />
        </Field>
        {!isNew && (
          <Field label="启用">
            <button
              className={cn('h-8 border px-3 text-[12px] transition-colors', enabled ? 'border-accent/50 text-accent' : 'border-line text-muted')}
              onClick={() => setEnabled(!enabled)}
              type="button"
            >
              {enabled ? '● 已启用（点击停用）' : '○ 已停用（点击启用）'}
            </button>
          </Field>
        )}

        {/* 插件声明的配置字段：动态渲染，Secret 字段遮罩；无声明则不显示该区 */}
        {(curType?.config_fields?.length ?? 0) > 0 && (
          <div className="border-t border-line pt-4">
            <div className="mono-label mb-3">实例配置（由插件类型声明）</div>
            <div className="space-y-4">
              {curType?.config_fields.map((f) => (
                <Field key={f.key} label={f.label} hint={f.help}>
                  <TextInput
                    mono
                    type={f.secret ? 'password' : 'text'}
                    value={config[f.key] ?? ''}
                    placeholder={f.placeholder}
                    onChange={(e) => setConfig((prev) => ({ ...prev, [f.key]: e.target.value }))}
                  />
                </Field>
              ))}
            </div>
          </div>
        )}

        {/* 准入规则编辑器 */}
        <div className="border-t border-line pt-4">
          <div className="mb-1 flex items-center">
            <div className="mono-label">准入规则（全部命中才放行 · AND）</div>
            <Btn
              variant="ghost"
              className="ml-auto h-7"
              onClick={() => setRules((rs) => [...rs, { field: claimFields[0]?.key ?? '', pattern: '' }])}
            >
              + 规则
            </Btn>
          </div>
          <div className="text-[11px] leading-5 text-muted">
            正则自动整串锚定（^…$）；字段缺失 = 不匹配；空规则集 = 不放行
          </div>
          <div className="mt-3 space-y-2">
            {rules.map((r, i) => (
              <div key={i} className="flex gap-2">
                <select
                  className="h-9 w-[200px] shrink-0 border border-line bg-bg px-2 text-[12.5px] text-text focus:border-accent/70 focus:outline-none"
                  value={r.field}
                  onChange={(e) =>
                    setRules((rs) => rs.map((x, j) => (j === i ? { ...x, field: e.target.value } : x)))
                  }
                >
                  {claimFields.map((cf) => (
                    <option key={cf.key} value={cf.key}>
                      {cf.label}
                    </option>
                  ))}
                </select>
                <TextInput
                  mono
                  value={r.pattern}
                  placeholder="^lyzz0612$"
                  onChange={(e) => setRules((rs) => rs.map((x, j) => (j === i ? { ...x, pattern: e.target.value } : x)))}
                />
                <Btn
                  variant="icon"
                  className="shrink-0 hover:text-danger"
                  onClick={() => setRules((rs) => rs.filter((_, j) => j !== i))}
                >
                  ✕
                </Btn>
              </div>
            ))}
          </div>
        </div>

        <ErrorText>{err}</ErrorText>
        <div className="flex justify-end gap-2 pt-2">
          <Btn onClick={onClose}>取消</Btn>
          <Btn variant="primary" disabled={busy || !name.trim()} onClick={() => void submit()}>
            {busy ? '保存中…' : '保存'}
          </Btn>
        </div>
      </div>
    </Drawer>
  )
}

/* ---------- 试一下 ---------- */

const SAMPLE_CLAIMS = `{
  "iss": "https://token.actions.githubusercontent.com",
  "aud": "https://secrets.example.com",
  "repository_owner": "lyzz0612",
  "repository_id": "987654321",
  "event_name": "push",
  "ref": "refs/heads/main"
}`

function TestDrawer({ provider, onClose }: { provider: Provider; onClose: () => void }) {
  const [claimsText, setClaimsText] = useState(SAMPLE_CLAIMS)
  const [out, setOut] = useState<TestOut | null>(null)
  const [err, setErr] = useState('')

  const run = async () => {
    setErr('')
    setOut(null)
    try {
      const claims = JSON.parse(claimsText) as Record<string, string>
      setOut(await api.testProvider(provider.id, claims))
    } catch (e) {
      setErr(e instanceof SyntaxError ? 'claims 不是合法 JSON' : e instanceof ApiError ? e.message : '执行失败')
    }
  }

  return (
    <Drawer open onOpenChange={(v) => !v && onClose()} title={`TEST · ${provider.name}`} width={560}>
      <div className="space-y-4">
        <Field label="贴一段 claims JSON（模拟一个请求的令牌声明）">
          <TextArea mono rows={10} value={claimsText} onChange={(e) => setClaimsText(e.target.value)} />
        </Field>
        <Btn variant="primary" onClick={() => void run()}>
          运行
        </Btn>
        <ErrorText>{err}</ErrorText>

        {out && (
          <div className="border border-line bg-bg">
            <div
              className={cn(
                'border-b border-line px-4 py-3 font-mono text-[13px]',
                out.allowed ? 'text-accent' : 'text-danger',
              )}
            >
              {out.allowed ? '✓ 放行' : '✗ 拒绝'}
            </div>
            <div>
              {(out.results ?? []).map((r, i) => (
                <div
                  key={i}
                  className="grid grid-cols-[180px_1fr_70px] items-center gap-3 border-b border-line/60 px-4 py-2 font-mono text-[12px] last:border-0"
                >
                  <span className="text-text">{r.field}</span>
                  <span className="truncate text-muted">/{r.pattern}/</span>
                  <span className={cn('text-right', r.missing ? 'text-warn' : r.hit ? 'text-accent' : 'text-danger')}>
                    {r.missing ? '缺失' : r.hit ? '✓' : '✗'}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </Drawer>
  )
}
