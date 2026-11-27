import { useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import type { Grant, Group, UserRow } from '../types'
import { Btn, Confirm, ErrorText, Field, Modal, cn } from '../ui'

interface UserGroupView {
  id: number
  name: string
  member_count: number
}

interface Row {
  grant: Grant
  subjectLabel: string
  targetLabel: string
}

export function GrantsPage({ onChanged }: { onChanged: () => void }) {
  const [view, setView] = useState<'bySubject' | 'byTarget'>('bySubject')
  const [rows, setRows] = useState<Row[]>([])
  const [users, setUsers] = useState<UserRow[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [userGroups, setUserGroups] = useState<UserGroupView[]>([])
  const [err, setErr] = useState('')
  const [addOpen, setAddOpen] = useState(false)
  const [delTarget, setDelTarget] = useState<Row | null>(null)
  const [busy, setBusy] = useState(false)

  const load = async () => {
    try {
      const [gs, us, grps, ugs] = await Promise.all([
        api.grants(),
        api.users(),
        api.groups(),
        api.userGroups() as Promise<UserGroupView[]>,
      ])
      setUsers(us)
      setGroups(grps)
      setUserGroups(ugs)
      const userLabel = (id: number) => {
        const u = us.find((x: UserRow) => x.id === id)
        return u ? `${u.display}` : `用户 #${id}（已删）`
      }
      const ugLabel = (id: number) => {
        const g = ugs.find((x: UserGroupView) => x.id === id)
        return g ? `${g.name}（${g.member_count} 成员）` : `用户组 #${id}（已删）`
      }
      const targetLabel = (g: Grant) => {
        if (g.target_kind === 'group') {
          const grp = grps.find((x: Group) => x.id === g.target_id)
          return grp ? `${grp.path}（含子孙）` : `密钥组 #${g.target_id}（已删）`
        }
        return `单条密钥 #${g.target_id}`
      }
      setRows(
        gs.map((g) => ({
          grant: g,
          subjectLabel: g.subject_kind === 'user' ? userLabel(g.subject_id) : ugLabel(g.subject_id),
          targetLabel: targetLabel(g),
        })),
      )
      setErr('')
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '加载失败')
    }
  }
  useEffect(() => {
    void load()
  }, [])

  const sorted = [...rows].sort((a, b) =>
    view === 'bySubject' ? a.subjectLabel.localeCompare(b.subjectLabel) : a.targetLabel.localeCompare(b.targetLabel),
  )

  return (
    <div className="mx-auto max-w-[1000px] px-8 py-6">
      <div className="mb-4 flex items-center gap-3">
        <div className="mono-label">GRANTS</div>
        <div className="flex border border-line">
          {(
            [
              ['bySubject', '按身份'],
              ['byTarget', '按密钥'],
            ] as const
          ).map(([v, label]) => (
            <button
              key={v}
              className={cn('px-3 py-1 text-[12px] transition-colors', view === v ? 'bg-surface2 text-accent' : 'text-muted hover:text-text')}
              onClick={() => setView(v)}
            >
              {label}
            </button>
          ))}
        </div>
        <Btn variant="primary" className="ml-auto" onClick={() => setAddOpen(true)}>
          + 新建授权
        </Btn>
      </div>

      <ErrorText>{err}</ErrorText>

      {rows.length === 0 && (
        <div className="border border-line bg-surface px-6 py-10 text-center">
          <div className="mono-label">EMPTY</div>
          <div className="mt-1 text-[12px] text-muted">还没有任何授权</div>
        </div>
      )}

      <div className="border border-line bg-surface">
        {sorted.map((r, i) => (
          <div
            key={r.grant.id}
            className="row-in flex items-center gap-3 border-b border-line/60 px-4 py-2.5 last:border-0 hover:bg-surface2/40"
            style={{ animationDelay: `${i * 30}ms` }}
          >
            <span className={cn('mono-label border px-1.5 py-0.5', r.grant.subject_kind === 'user' ? 'border-line2' : 'border-warn/40 text-warn')}>
              {r.grant.subject_kind === 'user' ? '个人' : '用户组'}
            </span>
            <span className="min-w-0 flex-1 truncate font-mono text-[12.5px] text-text">{r.subjectLabel}</span>
            <span className="text-line2">→</span>
            <span className="min-w-0 flex-1 truncate font-mono text-[12.5px] text-accent/90">{r.targetLabel}</span>
            <Btn variant="icon" className="hover:text-danger" title="删除授权" onClick={() => setDelTarget(r)}>
              ✕
            </Btn>
          </div>
        ))}
      </div>

      <AddGrantModal
        open={addOpen}
        onOpenChange={setAddOpen}
        users={users}
        userGroups={userGroups}
        groups={groups}
        onCreated={async () => {
          await load()
          onChanged()
        }}
      />

      <Confirm
        open={delTarget != null}
        onOpenChange={(v) => !v && setDelTarget(null)}
        busy={busy}
        title="REVOKE GRANT"
        confirmText="撤销"
        onConfirm={async () => {
          if (!delTarget) return
          setBusy(true)
          try {
            await api.deleteGrant(delTarget.grant.id)
            setDelTarget(null)
            await load()
            onChanged()
          } catch (e) {
            setErr(e instanceof ApiError ? e.message : '撤销失败')
          } finally {
            setBusy(false)
          }
        }}
      >
        撤销 <span className="font-mono text-warn">{delTarget?.subjectLabel}</span> →{' '}
        <span className="font-mono text-warn">{delTarget?.targetLabel}</span> 的授权？立即生效，调用方下次导出即拿不到。
      </Confirm>
    </div>
  )
}

function AddGrantModal({
  open,
  onOpenChange,
  users,
  userGroups,
  groups,
  onCreated,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  users: UserRow[]
  userGroups: UserGroupView[]
  groups: Group[]
  onCreated: () => Promise<void> | void
}) {
  const [subjectKind, setSubjectKind] = useState<'user' | 'group'>('user')
  const [subjectID, setSubjectID] = useState('')
  const [targetID, setTargetID] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const subjectOptions =
    subjectKind === 'user'
      ? users.map((u) => ({ id: u.id, label: `${u.display}（${u.provider_type}）` }))
      : userGroups.map((g) => ({ id: g.id, label: `${g.name}（${g.member_count} 成员）` }))

  return (
    <Modal open={open} onOpenChange={onOpenChange} title="NEW GRANT" width={460}>
      <div className="space-y-4">
        <Field label="主体（谁可以读）">
          <div className="flex gap-2">
            <select
              className="h-9 w-[110px] shrink-0 border border-line bg-bg px-2 text-[12.5px] text-text focus:border-accent/70 focus:outline-none"
              value={subjectKind}
              onChange={(e) => {
                setSubjectKind(e.target.value as 'user' | 'group')
                setSubjectID('')
              }}
            >
              <option value="user">用户</option>
              <option value="group">用户组</option>
            </select>
            <select
              className="h-9 min-w-0 flex-1 border border-line bg-bg px-2 text-[12.5px] text-text focus:border-accent/70 focus:outline-none"
              value={subjectID}
              onChange={(e) => setSubjectID(e.target.value)}
            >
              <option value="">选择…</option>
              {subjectOptions.map((o) => (
                <option key={o.id} value={String(o.id)}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
        </Field>
        <Field label="目标（能读什么）" hint="选密钥组 = 该组及其全部子孙环境">
          <select
            className="h-9 w-full border border-line bg-bg px-2 text-[12.5px] text-text focus:border-accent/70 focus:outline-none"
            value={targetID}
            onChange={(e) => setTargetID(e.target.value)}
          >
            <option value="">选择密钥组…</option>
            {groups.map((g) => (
              <option key={g.id} value={String(g.id)}>
                {g.path}
              </option>
            ))}
          </select>
        </Field>
        <ErrorText>{err}</ErrorText>
        <div className="flex justify-end gap-2">
          <Btn onClick={() => onOpenChange(false)}>取消</Btn>
          <Btn
            variant="primary"
            disabled={busy || !subjectID || !targetID}
            onClick={async () => {
              setBusy(true)
              setErr('')
              try {
                await api.createGrant(subjectKind, Number(subjectID), 'group', Number(targetID))
                setSubjectID('')
                setTargetID('')
                onOpenChange(false)
                await onCreated()
              } catch (e) {
                setErr(e instanceof ApiError ? e.message : '创建失败')
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? '创建中…' : '授权'}
          </Btn>
        </div>
      </div>
    </Modal>
  )
}
