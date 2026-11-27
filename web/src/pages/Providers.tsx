import { useEffect, useMemo, useState } from 'react'
import { api, ApiError } from '../api'
import type { PluginType, Provider, Rule, UserGroup } from '../types'
import { Btn, Confirm, CopyBtn, Drawer, ErrorText, Field, TextArea, TextInput, cn } from '../ui'

interface RuleResult {
  field: string
  pattern: string
  hit: boolean
  missing: boolean
}

interface TestOut {
  allowed: boolean
  results: RuleResult[]
  default_role: string
  auto_groups: { id: number; name: string }[]
}

export function ProvidersPage({ onChanged }: { onChanged: () => void }) {
  const [types, setTypes] = useState<PluginType[]>([])
  const [providers, setProviders] = useState<Provider[]>([])
  const [groups, setGroups] = useState<UserGroup[]>([])
  const [err, setErr] = useState('')
  const [editDrawer, setEditDrawer] = useState<Provider | 'new' | null>(null)
  const [delTarget, setDelTarget] = useState<Provider | null>(null)
  const [testTarget, setTestTarget] = useState<Provider | null>(null)
  const [busy, setBusy] = useState(false)

  const load = async () => {
    try {
      const [ts, ps, gs] = await Promise.all([api.plugins(), api.providers(), api.userGroups()])
      setTypes(ts)
      setProviders(ps)
      setGroups((gs ?? []) as UserGroup[])
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
  const groupName = (id: number) => groups.find((g) => g.id === id)?.name ?? `组 ${id}`

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
            一个实例 = 一个接入标识（实例名）+ 一份配置 + 一套准入规则
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
          <div className="mt-1 text-[12px] text-muted">
            还没有实例——GitHub / GitLab / Vercel 各建一条就能开始接入
          </div>
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
                <span className="font-mono text-[14px] text-text">{p.name}</span>
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
              <div className="mt-2 flex flex-wrap items-center gap-2 font-mono text-[11.5px] text-muted">
                <span>
                  POST <span className="text-accent">/v1/auth/{p.name}</span>
                </span>
                <CopyBtn getText={() => `${window.location.origin}/v1/auth/${p.name}`} />
                <span className="text-line2">|</span>
                <span>
                  验过之后：
                  <span className="text-text">{p.default_role === 'normal' ? 'normal（给真值）' : 'guest（只给 key 名）'}</span>
                </span>
                {p.auto_group_ids?.length > 0 && (
                  <span>· 自动入组 {p.auto_group_ids.map(groupName).join('、')}</span>
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
          groups={groups}
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
        用户身份与实例无关，不会被删除——只是少了一个入口；CI 里仍用 <span className="font-mono">{delTarget?.name}</span> 的调用会 401。
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
  groups,
  onClose,
  onSaved,
}: {
  state: Provider | 'new'
  types: PluginType[]
  groups: UserGroup[]
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
    if (!existing) return [{ field: 'aud', pattern: '' }]
    try {
      const r = JSON.parse(existing.rules_json) as Rule[]
      return r.length ? r : [{ field: 'aud', pattern: '' }]
    } catch {
      return [{ field: 'aud', pattern: '' }]
    }
  })
  const [defaultRole, setDefaultRole] = useState(existing?.default_role ?? 'guest')
  const [autoGroupIds, setAutoGroupIds] = useState<number[]>(existing?.auto_group_ids ?? [])
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const curType = useMemo(() => types.find((t) => t.type === type), [types, type])
  const claimFields = curType?.claim_fields ?? []

  const toggleGroup = (id: number) =>
    setAutoGroupIds((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]))

  const submit = async () => {
    setBusy(true)
    setErr('')
    try {
      if (isNew) {
        await api.createProvider(
          type,
          name.trim(),
          config,
          rules.filter((r) => r.field && r.pattern),
          defaultRole,
          autoGroupIds,
        )
      } else {
        await api.updateProvider(
          existing!.id,
          name.trim(),
          enabled,
          config,
          rules.filter((r) => r.field && r.pattern),
          defaultRole,
          autoGroupIds,
        )
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
    <Drawer open onOpenChange={(v) => !v && onClose()} title={isNew ? 'NEW PROVIDER' : 'EDIT PROVIDER'} width={580}>
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
        <Field
          label="接入标识（实例名）"
          hint="CI 侧调 POST /v1/auth/{这个名字}；只能用字母、数字、点、下划线、连字符，全局唯一。改名等于换标识。"
        >
          <TextInput mono value={name} placeholder="gitlab-m68" onChange={(e) => setName(e.target.value)} />
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

        {/* 验过之后：自动登记的分级与组 */}
        <div className="border-t border-line pt-4">
          <div className="mono-label mb-1">验过之后（只在该身份首次登记时生效）</div>
          <div className="text-[11px] leading-5 text-muted">
            之后以后台为准：这里改了分级 / 组，不会覆盖已经登记过的用户。
          </div>
          <div className="mt-3 space-y-4">
            <Field label="默认分级" hint="guest 只拿到 key 名与固定假值；normal 拿真实值">
              <div className="flex gap-1">
                {(['guest', 'normal'] as const).map((r) => (
                  <button
                    key={r}
                    type="button"
                    className={cn(
                      'h-8 flex-1 border text-[12px] transition-colors',
                      defaultRole === r ? 'border-accent/60 bg-accent/5 text-accent' : 'border-line text-muted hover:border-line2',
                    )}
                    onClick={() => setDefaultRole(r)}
                  >
                    {r === 'guest' ? 'guest（只给 key 名）' : 'normal（给真值）'}
                  </button>
                ))}
              </div>
            </Field>
            <Field label="自动加入用户组（可多选）" hint="新身份过准入后自动进这些组，组上的授权立即生效">
              {groups.length === 0 ? (
                <div className="text-[12px] text-muted">还没有用户组——先去「用户组」页建一个</div>
              ) : (
                <div className="border border-line bg-bg">
                  {groups.map((g) => {
                    const on = autoGroupIds.includes(g.id)
                    return (
                      <button
                        key={g.id}
                        type="button"
                        className="flex w-full items-center gap-3 border-b border-line/60 px-3 py-2 text-left last:border-0 hover:bg-surface2/40"
                        onClick={() => toggleGroup(g.id)}
                      >
                        <span
                          className={cn(
                            'flex h-4 w-4 shrink-0 items-center justify-center border font-mono text-[11px]',
                            on ? 'border-accent bg-accent text-bg' : 'border-line2 text-transparent',
                          )}
                        >
                          ✓
                        </span>
                        <span className="truncate text-[12.5px] text-text">{g.name}</span>
                        <span className="ml-auto font-mono text-[11px] text-muted">{g.member_count} 成员</span>
                      </button>
                    )
                  })}
                </div>
              )}
            </Field>
          </div>
        </div>

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

const SAMPLE_CLAIMS: Record<string, string> = {
  github_oidc: `{
  "iss": "https://token.actions.githubusercontent.com",
  "aud": "https://secrets.example.com",
  "repository_owner": "lyzz0612",
  "repository_id": "987654321",
  "event_name": "push",
  "ref": "refs/heads/main"
}`,
  gitlab_oidc: `{
  "iss": "https://gitlab.example.com",
  "aud": "https://secrets.example.com",
  "project_id": "20",
  "project_path": "my-group/my-project",
  "ref": "main",
  "ref_type": "branch",
  "ref_protected": "true",
  "pipeline_source": "push"
}`,
  vercel_oidc: `{
  "iss": "https://oidc.vercel.com/acme",
  "aud": "https://vercel.com/acme",
  "owner": "acme",
  "project": "acme_website",
  "project_id": "prj_xxx",
  "environment": "production"
}`,
}

function TestDrawer({ provider, onClose }: { provider: Provider; onClose: () => void }) {
  const [claimsText, setClaimsText] = useState(SAMPLE_CLAIMS[provider.type] ?? '{}')
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
        <div className="text-[12px] text-muted">
          只跑准入规则（验签在真实请求里做）。通过后的落地区间也一并算给你看。
        </div>
        <Field label="贴一段 claims JSON（模拟一个请求的令牌声明）">
          <TextArea mono rows={11} value={claimsText} onChange={(e) => setClaimsText(e.target.value)} />
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
            {out.allowed && (
              <div className="border-t border-line px-4 py-3 text-[12px] leading-6 text-muted">
                首次登记会写成 <span className="font-mono text-accent">{out.default_role}</span>
                {out.auto_groups?.length
                  ? `，并加入 ${out.auto_groups.map((g) => g.name).join('、')}`
                  : '，不加入任何用户组'}
              </div>
            )}
          </div>
        )}
      </div>
    </Drawer>
  )
}