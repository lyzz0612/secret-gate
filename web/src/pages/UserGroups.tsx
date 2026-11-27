import { useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import type { UserRow } from '../types'
import { Btn, Confirm, Drawer, ErrorText, Field, Modal, TextArea, TextInput, cn } from '../ui'

interface GroupView {
  id: number
  name: string
  description: string
  created_at: string
  member_count: number
}

export function UserGroupsPage({ onChanged }: { onChanged: () => void }) {
  const [groups, setGroups] = useState<GroupView[]>([])
  const [err, setErr] = useState('')
  const [newOpen, setNewOpen] = useState(false)
  const [membersOf, setMembersOf] = useState<GroupView | null>(null)
  const [delTarget, setDelTarget] = useState<GroupView | null>(null)
  const [busy, setBusy] = useState(false)

  const load = async () => {
    try {
      setGroups((await api.userGroups()) as GroupView[])
      setErr('')
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '加载失败')
    }
  }
  useEffect(() => {
    void load()
  }, [])

  return (
    <div className="mx-auto max-w-[900px] px-8 py-6">
      <div className="mb-4 flex items-center">
        <div>
          <div className="mono-label">USER GROUPS</div>
          <div className="mt-1 text-[13px] text-muted">按组批量发授权：组授权 → 组内全部成员可读</div>
        </div>
        <Btn variant="primary" className="ml-auto" onClick={() => setNewOpen(true)}>
          + 新建用户组
        </Btn>
      </div>

      <ErrorText>{err}</ErrorText>

      {groups.length === 0 && (
        <div className="border border-line bg-surface px-6 py-10 text-center">
          <div className="mono-label">EMPTY</div>
          <div className="mt-1 text-[12px] text-muted">还没有用户组</div>
        </div>
      )}

      <div className="border border-line bg-surface">
        {groups.map((g, i) => (
          <div
            key={g.id}
            className="row-in flex items-center gap-3 border-b border-line/60 px-4 py-3 last:border-0 hover:bg-surface2/40"
            style={{ animationDelay: `${i * 30}ms` }}
          >
            <span className="h-1.5 w-1.5 bg-line2" />
            <div className="min-w-0 flex-1">
              <div className="text-[13.5px] text-text">{g.name}</div>
              {g.description && <div className="truncate text-[11.5px] text-muted">{g.description}</div>}
            </div>
            <span className="font-mono text-[12px] text-muted">{g.member_count} 成员</span>
            <div className="flex gap-1">
              <Btn variant="ghost" className="h-7" onClick={() => setMembersOf(g)}>
                成员
              </Btn>
              <Btn variant="danger" className="h-7" onClick={() => setDelTarget(g)}>
                删除
              </Btn>
            </div>
          </div>
        ))}
      </div>

      <NewGroupModal
        open={newOpen}
        onOpenChange={setNewOpen}
        onCreated={async () => {
          await load()
          onChanged()
        }}
      />

      {membersOf && (
        <MembersDrawer
          group={membersOf}
          onClose={() => setMembersOf(null)}
          onChanged={async () => {
            await load()
          }}
        />
      )}

      <Confirm
        open={delTarget != null}
        onOpenChange={(v) => !v && setDelTarget(null)}
        busy={busy}
        title="DELETE USER GROUP"
        onConfirm={async () => {
          if (!delTarget) return
          setBusy(true)
          try {
            await api.deleteUserGroup(delTarget.id)
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
        确定删除用户组 <span className="font-mono text-warn">{delTarget?.name}</span>？
        组授权与成员关系会一并删除，组内成员立即失去这些授权。
      </Confirm>
    </div>
  )
}

function NewGroupModal({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onCreated: () => Promise<void> | void
}) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  return (
    <Modal open={open} onOpenChange={onOpenChange} title="NEW USER GROUP">
      <div className="space-y-4">
        <Field label="组名">
          <TextInput value={name} placeholder="CI 集群" onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="描述（可选）">
          <TextArea rows={2} value={description} placeholder="所有构建机" onChange={(e) => setDescription(e.target.value)} />
        </Field>
        <ErrorText>{err}</ErrorText>
        <div className="flex justify-end gap-2">
          <Btn onClick={() => onOpenChange(false)}>取消</Btn>
          <Btn
            variant="primary"
            disabled={busy || !name.trim()}
            onClick={async () => {
              setBusy(true)
              setErr('')
              try {
                await api.createUserGroup(name.trim(), description.trim())
                setName('')
                setDescription('')
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

function MembersDrawer({
  group,
  onClose,
  onChanged,
}: {
  group: GroupView
  onClose: () => void
  onChanged: () => Promise<void> | void
}) {
  const [users, setUsers] = useState<UserRow[]>([])
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    void (async () => {
      try {
        const [all, members] = await Promise.all([api.users(), api.userGroupMembers(group.id)])
        setUsers(all)
        setSelected(new Set(members.map((m) => m.id)))
      } catch (e) {
        setErr(e instanceof ApiError ? e.message : '加载失败')
      }
    })()
  }, [group])

  const toggle = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  return (
    <Drawer open onOpenChange={(v) => !v && onClose()} title={`MEMBERS · ${group.name}`}>
      <div className="space-y-4">
        <div className="text-[12px] text-muted">勾选即成员，取消勾选即移出——保存后立即生效</div>
        {users.length === 0 && <div className="text-[12px] text-muted">还没有任何用户来敲门</div>}
        <div className="border border-line bg-bg">
          {users.map((u, i) => {
            const on = selected.has(u.id)
            return (
              <button
                key={u.id}
                className="row-in flex w-full items-center gap-3 border-b border-line/60 px-3 py-2.5 text-left last:border-0 hover:bg-surface2/40"
                style={{ animationDelay: `${i * 30}ms` }}
                onClick={() => toggle(u.id)}
              >
                <span
                  className={cn(
                    'flex h-4 w-4 shrink-0 items-center justify-center border font-mono text-[11px]',
                    on ? 'border-accent bg-accent text-bg' : 'border-line2 text-transparent',
                  )}
                >
                  ✓
                </span>
                <div className="min-w-0 flex-1">
                  <div className="truncate font-mono text-[12.5px] text-text">{u.display}</div>
                  <div className="truncate font-mono text-[10.5px] text-muted">
                    {u.provider_type}:{u.external_id}
                  </div>
                </div>
                {u.status === 'pending' && <span className="mono-label text-warn">pending</span>}
              </button>
            )
          })}
        </div>
        <ErrorText>{err}</ErrorText>
        <div className="flex justify-end gap-2">
          <Btn onClick={onClose}>取消</Btn>
          <Btn
            variant="primary"
            disabled={busy}
            onClick={async () => {
              setBusy(true)
              setErr('')
              try {
                await api.setUserGroupMembers(
                  group.id,
                  users.filter((u) => selected.has(u.id)).map((u) => u.id),
                )
                onChanged()
                onClose()
              } catch (e) {
                setErr(e instanceof ApiError ? e.message : '保存失败')
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? '保存中…' : `保存（${selected.size} 成员）`}
          </Btn>
        </div>
      </div>
    </Drawer>
  )
}
