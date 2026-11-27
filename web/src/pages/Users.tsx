import { useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import type { Grant, Group, PluginType, UserRow } from '../types'
import { Btn, Confirm, CopyBtn, Drawer, ErrorText, Field, Modal, TextInput, cn } from '../ui'

export function UsersPage({ onChanged }: { onChanged: () => void }) {
  const [users, setUsers] = useState<UserRow[]>([])
  const [rejections, setRejections] = useState<{ display: string; reason: string; ts: string }[]>([])
  const [err, setErr] = useState('')
  const [detail, setDetail] = useState<UserRow | null>(null)
  const [newOpen, setNewOpen] = useState(false)
  const [delTarget, setDelTarget] = useState<UserRow | null>(null)
  const [busy, setBusy] = useState(false)

  const load = async () => {
    try {
      setUsers(await api.users())
      setRejections(await api.rejections())
      setErr('')
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '加载失败')
    }
  }
  useEffect(() => {
    void load()
  }, [])

  const toggleRole = async (u: UserRow) => {
    const role = u.role === 'normal' ? 'guest' : 'normal'
    try {
      await api.setUserRole(u.id, role)
      await load()
      onChanged()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '切换失败')
    }
  }

  return (
    <div className="mx-auto max-w-[1000px] px-8 py-6">
      <div className="mb-4 flex items-center gap-2">
        <div className="mono-label">USERS</div>
        <div className="text-[12px] text-muted">
          appkey / 账号密码用户在后台创建；实例型（GitHub OIDC）过准入自动登记；默认 guest 只能看 key 名
        </div>
        <Btn variant="primary" className="ml-auto" onClick={() => setNewOpen(true)}>
          + 新建用户
        </Btn>
      </div>

      <ErrorText>{err}</ErrorText>

      {rejections.length > 0 && (
        <div className="mb-4 border-l-2 border-warn bg-warn/5 px-4 py-3">
          <div className="mono-label mb-1.5 text-warn">RECENT REJECTIONS · 最近被拒（{rejections.length}）</div>
          {rejections.slice(0, 3).map((r, i) => (
            <div key={i} className="font-mono text-[11.5px] leading-5 text-muted">
              {r.display || '未知'} — {r.reason}
            </div>
          ))}
        </div>
      )}

      {users.length === 0 && (
        <div className="border border-line bg-surface px-6 py-10 text-center">
          <div className="mono-label">EMPTY</div>
          <div className="mt-1 text-[12px] text-muted">还没有任何用户——建一个 appkey 用户开始接入</div>
        </div>
      )}

      <div className="border border-line bg-surface">
        {users.map((u, i) => (
          <div
            key={u.id}
            className="row-in grid grid-cols-[minmax(200px,1.5fr)_120px_90px_150px] items-center gap-4 border-b border-line/60 px-4 last:border-0 hover:bg-surface2/40"
            style={{ animationDelay: `${i * 30}ms` }}
          >
            <div className="flex items-center gap-2 py-2">
              <div className="min-w-0">
                <div className="truncate font-mono text-[13px] text-text">{u.display}</div>
                <div className="truncate font-mono text-[11px] text-muted">
                  {u.provider_type}:{u.external_id}
                </div>
              </div>
            </div>
            <div>
              <button
                className={cn(
                  'mono-label border px-2 py-1 transition-colors',
                  u.role === 'normal'
                    ? 'border-accent/50 text-accent hover:bg-accent/10'
                    : 'border-line text-muted hover:border-warn/50 hover:text-warn',
                )}
                title={u.role === 'normal' ? '点击降为 guest（导出变假值）' : '点击提为 normal（导出真值）'}
                onClick={() => void toggleRole(u)}
              >
                {u.role === 'normal' ? '● normal' : '○ guest'}
              </button>
            </div>
            <div className="font-mono text-[11px] text-muted">{u.builtin ? u.cred_kind : '自动登记'}</div>
            <div className="flex items-center justify-end gap-1 py-2">
              <Btn variant="ghost" className="h-7" onClick={() => setDetail(u)}>
                详情 / 授权
              </Btn>
              <Btn variant="danger" className="h-7" onClick={() => setDelTarget(u)}>
                删除
              </Btn>
            </div>
          </div>
        ))}
      </div>

      {detail && (
        <UserDrawer
          user={detail}
          onClose={() => setDetail(null)}
          onChanged={async () => {
            await load()
            onChanged()
          }}
        />
      )}

      <NewUserModal
        open={newOpen}
        onOpenChange={setNewOpen}
        onCreated={async () => {
          await load()
          onChanged()
        }}
      />

      <Confirm
        open={delTarget != null}
        onOpenChange={(v) => !v && setDelTarget(null)}
        busy={busy}
        title="DELETE USER"
        onConfirm={async () => {
          if (!delTarget) return
          setBusy(true)
          try {
            await api.deleteUser(delTarget.id)
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
        确定删除 <span className="font-mono text-warn">{delTarget?.display}</span>？
        它的个人授权与组成员关系会一并删除。实例型用户下次过准入会重新登记。
      </Confirm>
    </div>
  )
}

/* ---------- 新建用户（appkey / 账号密码） ---------- */

function NewUserModal({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onCreated: () => Promise<void> | void
}) {
  const [types, setTypes] = useState<PluginType[]>([])
  const [type, setType] = useState('')
  const [externalId, setExternalId] = useState('')
  const [display, setDisplay] = useState('')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<{ user: UserRow; credential: string } | null>(null)

  useEffect(() => {
    if (!open) return
    setErr('')
    setCreated(null)
    api
      .plugins()
      .then((ts) => {
        const builtin = ts.filter((t) => t.builtin)
        setTypes(builtin)
        if (!type && builtin.length > 0) setType(builtin[0].type)
      })
      .catch((e) => setErr(e instanceof ApiError ? e.message : '加载插件类型失败'))
  }, [open])

  const isPassword = type === 'password'

  const submit = async () => {
    setBusy(true)
    setErr('')
    try {
      const out = await api.createUser(type, externalId.trim(), isPassword ? password : '', display.trim())
      setCreated(out)
      setExternalId('')
      setDisplay('')
      setPassword('')
      await onCreated()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal open={open} onOpenChange={onOpenChange} title="NEW USER" width={480}>
      <div className="space-y-4">
        <Field label="用户类型" hint="appkey / 账号密码是内置类型；GitHub OIDC 用户由工作流敲门自动登记">
          <select
            className="h-9 w-full border border-line bg-bg px-2 text-[12.5px] text-text focus:border-accent/70 focus:outline-none"
            value={type}
            onChange={(e) => setType(e.target.value)}
          >
            {types.map((t) => (
              <option key={t.type} value={t.type}>
                {t.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label={isPassword ? '用户名' : '标识名'} hint={isPassword ? '调用方 Basic auth 用的用户名' : '仅用于后台识别，不出现在请求里'}>
          <TextInput
            mono
            value={externalId}
            placeholder={isPassword ? 'ci-runner' : 'ci-app'}
            onChange={(e) => setExternalId(e.target.value)}
          />
        </Field>
        {isPassword && (
          <Field label="口令" hint="加密入库；后台随时可查看与轮换">
            <TextInput mono type="password" value={password} placeholder="••••••••" onChange={(e) => setPassword(e.target.value)} />
          </Field>
        )}
        <Field label="显示名（可选，留空取标识名）">
          <TextInput value={display} placeholder={isPassword ? 'ci-runner' : '构建机 A'} onChange={(e) => setDisplay(e.target.value)} />
        </Field>
        <div className="text-[11.5px] leading-5 text-muted">
          新用户默认 <span className="text-warn">guest</span>：能看到 key 名但导出值为固定假值；建好后去「详情 / 授权」配授权并按需提级 normal。
        </div>
        <ErrorText>{err}</ErrorText>

        {created && (
          <div className="border-l-2 border-accent bg-accent/5 px-4 py-3">
            <div className="mono-label mb-1.5 text-accent">CREATED · 凭证已生成</div>
            <div className="mb-2 text-[12px] text-muted">
              {isPassword ? '口令（后台可随时查看 / 轮换）' : 'AppKey（后台可随时查看 / 轮换）'}：
            </div>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate border border-line bg-bg px-3 py-2 font-mono text-[12px] text-text">
                {created.credential}
              </code>
              <CopyBtn getText={() => created.credential} />
            </div>
            <div className="mt-2 text-[11.5px] leading-5 text-muted">
              调用方用法：<code className="font-mono text-accent">
                {isPassword ? `curl -u <用户名>:<口令> -X POST .../v1/auth` : `curl -H "X-App-Key: <key>" -X POST .../v1/auth`}
              </code>
            </div>
          </div>
        )}

        <div className="flex justify-end gap-2">
          <Btn onClick={() => onOpenChange(false)}>关闭</Btn>
          <Btn
            variant="primary"
            disabled={busy || !type || !externalId.trim() || (isPassword && !password)}
            onClick={() => void submit()}
          >
            {busy ? '创建中…' : '创建'}
          </Btn>
        </div>
      </div>
    </Modal>
  )
}

/* ---------- 用户详情抽屉：凭证管理 + 授权 ---------- */

function UserDrawer({
  user,
  onClose,
  onChanged,
}: {
  user: UserRow
  onClose: () => void
  onChanged: () => Promise<void> | void
}) {
  const [grants, setGrants] = useState<Grant[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [newTarget, setNewTarget] = useState('')
  const [cred, setCred] = useState('')
  const [rotating, setRotating] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const load = async () => {
    try {
      const [gs, grps] = await Promise.all([api.userGrants(user.id), api.groups()])
      setGrants(gs)
      setGroups(grps)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '加载失败')
    }
  }
  useEffect(() => {
    void load()
  }, [user])

  const grantLabel = (g: Grant) => {
    if (g.target_kind === 'group') {
      const grp = groups.find((x) => x.id === g.target_id)
      return grp ? `密钥组 ${grp.path}（含子孙）` : `密钥组 #${g.target_id}（已删）`
    }
    return `单条密钥 #${g.target_id}`
  }

  const reveal = async () => {
    setErr('')
    try {
      const out = await api.revealCredential(user.id)
      setCred(out.value)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '查看失败')
    }
  }

  const rotate = async () => {
    setErr('')
    setRotating(true)
    try {
      const out = await api.rotateCredential(user.id, '')
      setCred(out.value)
      await onChanged()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '轮换失败')
    } finally {
      setRotating(false)
    }
  }

  return (
    <Drawer open onOpenChange={(v) => !v && onClose()} title={`USER · ${user.display}`} width={520}>
      <div className="space-y-5">
        <div className="grid grid-cols-[110px_1fr] gap-x-4 gap-y-1.5 border border-line bg-bg px-4 py-3 font-mono text-[12px]">
          <span className="text-muted">类型</span>
          <span>{user.provider_type}</span>
          <span className="text-muted">{user.builtin ? '标识' : '外部 ID'}</span>
          <span>{user.external_id}</span>
          <span className="text-muted">分级</span>
          <span className={user.role === 'normal' ? 'text-accent' : 'text-warn'}>{user.role}</span>
          <span className="text-muted">最近来访</span>
          <span>{user.last_seen_at || '—'}</span>
        </div>

        {user.builtin && (
          <div>
            <div className="mono-label mb-2">凭证</div>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate border border-line bg-bg px-3 py-2 font-mono text-[12px] text-text">
                {cred || '••••••••（点击查看）'}
              </code>
              <CopyBtn getText={() => cred} className={cred ? '' : 'pointer-events-none opacity-40'} />
              <Btn variant="ghost" className="h-9" onClick={() => void reveal()}>
                查看
              </Btn>
              <Btn
                variant="ghost"
                className="h-9 hover:text-danger"
                disabled={rotating}
                onClick={() => void rotate()}
              >
                {rotating ? '轮换中…' : '轮换'}
              </Btn>
            </div>
            <div className="mt-1.5 text-[11px] text-muted">
              轮换 = 生成全新凭证，旧值立即失效。{user.cred_kind === 'password' ? '留空自动生成随机口令。' : ''}
            </div>
          </div>
        )}

        <div>
          <div className="mono-label mb-2">授权（个人）</div>
          {grants.length === 0 && <div className="text-[12px] text-muted">无个人授权（可读密钥 = 0）</div>}
          <div className="space-y-1">
            {grants.map((g) => (
              <div key={g.id} className="flex items-center gap-2 border border-line bg-bg px-3 py-2">
                <span className="min-w-0 flex-1 truncate text-[12.5px]">{grantLabel(g)}</span>
                <Btn
                  variant="icon"
                  className="hover:text-danger"
                  onClick={async () => {
                    try {
                      await api.deleteGrant(g.id)
                      await load()
                      await onChanged()
                    } catch (e) {
                      setErr(e instanceof ApiError ? e.message : '删除失败')
                    }
                  }}
                >
                  ✕
                </Btn>
              </div>
            ))}
          </div>

          <div className="mt-3">
            <Field label="添加授权（选择密钥组）">
              <div className="flex gap-2">
                <select
                  className="h-9 min-w-0 flex-1 border border-line bg-bg px-2 text-[12.5px] text-text focus:border-accent/70 focus:outline-none"
                  value={newTarget}
                  onChange={(e) => setNewTarget(e.target.value)}
                >
                  <option value="">选择密钥组…</option>
                  {groups.map((g) => (
                    <option key={g.id} value={String(g.id)}>
                      {g.path}
                    </option>
                  ))}
                </select>
                <Btn
                  variant="primary"
                  disabled={!newTarget || busy}
                  onClick={async () => {
                    setBusy(true)
                    setErr('')
                    try {
                      await api.createGrant('user', user.id, 'group', Number(newTarget))
                      setNewTarget('')
                      await load()
                      await onChanged()
                    } catch (e) {
                      setErr(e instanceof ApiError ? e.message : '授权失败')
                    } finally {
                      setBusy(false)
                    }
                  }}
                >
                  授权
                </Btn>
              </div>
            </Field>
          </div>
        </div>

        <ErrorText>{err}</ErrorText>
      </div>
    </Drawer>
  )
}
