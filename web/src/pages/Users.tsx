import { useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import type { Grant, Group, PluginType, UserRow } from '../types'
import { Btn, Confirm, Drawer, ErrorText, Field, Modal, TextInput } from '../ui'

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

  return (
    <div className="mx-auto max-w-[1000px] px-8 py-6">
      <div className="mb-4 flex items-center gap-2">
        <div className="mono-label">USERS</div>
        <div className="text-[12px] text-muted">过了准入规则的身份自动登记；能读什么由授权决定</div>
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
          <div className="mt-1 text-[12px] text-muted">还没有任何身份进来过</div>
        </div>
      )}

      <div className="border border-line bg-surface">
        {users.map((u, i) => (
          <div
            key={u.id}
            className="row-in grid grid-cols-[minmax(220px,1.5fr)_110px_150px] items-center gap-4 border-b border-line/60 px-4 last:border-0 hover:bg-surface2/40"
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
            <div className="font-mono text-[11px] text-muted">{u.kind}</div>
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
        它的个人授权与组成员关系会一并删除。它下次过准入会重新登记。
      </Confirm>
    </div>
  )
}

/* ---------- 新建用户（预配置 / 测试） ---------- */

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
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!open) return
    setErr('')
    api
      .plugins()
      .then((ts) => {
        setTypes(ts)
        if (!type && ts.length > 0) setType(ts[0].type)
      })
      .catch((e) => setErr(e instanceof ApiError ? e.message : '加载插件类型失败'))
  }, [open])

  const isGithub = type === 'github_oidc'

  return (
    <Modal open={open} onOpenChange={onOpenChange} title="NEW USER" width={460}>
      <div className="space-y-4">
        <Field label="插件类型">
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
        <Field
          label="外部 ID"
          hint={
            isGithub
              ? '仓库的数字 repository_id（GitHub 仓库页 URL 或 API 可查；将来真实令牌敲门会按此键命中同一条记录）'
              : '口令类实例配置的用户名'
          }
        >
          <TextInput
            mono
            value={externalId}
            placeholder={isGithub ? '987654321' : 'ci-runner'}
            onChange={(e) => setExternalId(e.target.value)}
          />
        </Field>
        <Field label="显示名（可选，留空取外部 ID）">
          <TextInput value={display} placeholder={isGithub ? 'lyzz0612/minigames' : 'ci-runner'} onChange={(e) => setDisplay(e.target.value)} />
        </Field>
        <div className="text-[11.5px] leading-5 text-muted">
          建好后即可在「详情 / 授权」里配授权——适合在真实请求到来前预配置。
        </div>
        <ErrorText>{err}</ErrorText>
        <div className="flex justify-end gap-2">
          <Btn onClick={() => onOpenChange(false)}>取消</Btn>
          <Btn
            variant="primary"
            disabled={busy || !type || !externalId.trim()}
            onClick={async () => {
              setBusy(true)
              setErr('')
              try {
                await api.createUser(type, externalId.trim(), display.trim())
                setExternalId('')
                setDisplay('')
                onOpenChange(false)
                await onCreated()
              } catch (e) {
                setErr(e instanceof ApiError ? e.message : '创建失败')
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? '创建中…' : '创建'}
          </Btn>
        </div>
      </div>
    </Modal>
  )
}

/* ---------- 用户详情抽屉：授权管理 ---------- */

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

  return (
    <Drawer open onOpenChange={(v) => !v && onClose()} title={`USER · ${user.display}`} width={520}>
      <div className="space-y-5">
        <div className="grid grid-cols-[110px_1fr] gap-x-4 gap-y-1.5 border border-line bg-bg px-4 py-3 font-mono text-[12px]">
          <span className="text-muted">类型</span>
          <span>{user.provider_type}</span>
          <span className="text-muted">外部 ID</span>
          <span>{user.external_id}</span>
          <span className="text-muted">最近来访</span>
          <span>{user.last_seen_at || '—'}</span>
        </div>

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
