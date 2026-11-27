import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, ApiError } from '../api'
import type { Group, SecretMeta } from '../types'
import { Btn, Confirm, CopyBtn, Drawer, EmptyState, ErrorText, Field, Modal, TextArea, TextInput, cn } from '../ui'

interface GNode extends Group {
  children: Group[]
}

function buildTree(groups: Group[]): GNode[] {
  const roots: GNode[] = []
  const byPath = new Map<string, GNode>()
  for (const g of groups) {
    if (!g.path.includes('/')) {
      const node = { ...g, children: [] as Group[] }
      roots.push(node)
      byPath.set(g.path, node)
    }
  }
  for (const g of groups) {
    if (!g.path.includes('/')) continue
    const parent = g.path.slice(0, g.path.lastIndexOf('/'))
    const pnode = byPath.get(parent)
    if (pnode) pnode.children.push(g)
    else {
      // 父不存在（理论上不出现）：当根展示
      roots.push({ ...g, children: [] })
    }
  }
  return roots
}

export function SecretsPage({ onChanged }: { onChanged: () => void }) {
  const [groups, setGroups] = useState<Group[]>([])
  const [sel, setSel] = useState<number | null>(null)
  const [secrets, setSecrets] = useState<SecretMeta[]>([])
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  // 对话框
  const [newGroupOpen, setNewGroupOpen] = useState(false)
  const [secretDrawer, setSecretDrawer] = useState<null | { mode: 'add' | 'edit'; key: string }>(null)
  const [delGroup, setDelGroup] = useState<Group | null>(null)
  const [delSecret, setDelSecret] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const tree = useMemo(() => buildTree(groups), [groups])
  const selGroup = groups.find((g) => g.id === sel) ?? null

  const loadGroups = useCallback(async () => {
    try {
      const gs = (await api.groups()) as Group[]
      setGroups(gs)
      return gs
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '加载失败')
      return []
    }
  }, [])

  const loadSecrets = useCallback(async (gid: number) => {
    try {
      const ss = (await api.secrets(gid)) as SecretMeta[]
      setSecrets(ss)
      setRevealed({})
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '加载失败')
    }
  }, [])

  useEffect(() => {
    void (async () => {
      setLoading(true)
      const gs = await loadGroups()
      // 默认选中：第一个有子组的项目选其第一个子组，否则选第一个组
      const t = buildTree(gs)
      let first: number | null = null
      if (t.length > 0) {
        first = t[0].children.length > 0 ? t[0].children[0].id : t[0].id
      }
      setSel(first)
      if (first != null) await loadSecrets(first)
      setLoading(false)
    })()
  }, [loadGroups, loadSecrets])

  useEffect(() => {
    if (sel != null) void loadSecrets(sel)
  }, [sel, loadSecrets])

  const toggleReveal = async (key: string) => {
    if (revealed[key] !== undefined) {
      const next = { ...revealed }
      delete next[key]
      setRevealed(next)
      return
    }
    try {
      const r = await api.reveal(sel!, key)
      setRevealed((prev) => ({ ...prev, [key]: r.value as string }))
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '解密失败')
    }
  }

  const copyText = async (key: string) => {
    const v = revealed[key] ?? ((await api.reveal(sel!, key)).value as string)
    if (revealed[key] === undefined) setRevealed((prev) => ({ ...prev, [key]: v }))
    return v
  }

  const descendants = (g: Group) => groups.filter((x) => x.path !== g.path && x.path.startsWith(g.path + '/'))

  return (
    <div className="flex h-full min-h-0">
      {/* 左：分组树 */}
      <div className="flex w-[260px] shrink-0 flex-col border-r border-line">
        <div className="flex items-center justify-between border-b border-line px-4 py-3">
          <span className="mono-label">项目 / 环境</span>
          <Btn variant="icon" title="新建分组" onClick={() => setNewGroupOpen(true)}>
            +
          </Btn>
        </div>
        <div className="flex-1 overflow-y-auto p-2">
          {tree.map((root) => (
            <div key={root.id} className="mb-1">
              <button
                onClick={() => setSel(root.id)}
                className={cn(
                  'flex w-full items-center gap-2 px-2 py-[7px] text-left text-[13px] transition-colors',
                  sel === root.id ? 'bg-surface2 text-accent' : 'text-text hover:bg-surface2/60',
                )}
              >
                <span className={cn('h-1 w-1', sel === root.id ? 'bg-accent' : 'bg-line2')} />
                <span className="font-mono">{root.name || root.path}</span>
              </button>
              {root.children.map((c) => (
                <button
                  key={c.id}
                  onClick={() => setSel(c.id)}
                  className={cn(
                    'flex w-full items-center gap-2 py-[6px] pl-7 pr-2 text-left text-[12.5px] transition-colors',
                    sel === c.id ? 'bg-surface2 text-accent' : 'text-muted hover:bg-surface2/60 hover:text-text',
                  )}
                >
                  <span className={cn('h-1 w-1', sel === c.id ? 'bg-accent' : 'bg-line2')} />
                  <span className="font-mono">{c.name || c.path.split('/').pop()}</span>
                </button>
              ))}
            </div>
          ))}
          {tree.length === 0 && !loading && (
            <div className="px-3 py-6 text-center text-[12px] text-muted">
              还没有分组
              <br />
              点右上 + 新建
            </div>
          )}
        </div>
      </div>

      {/* 右：密钥列表 */}
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-[52px] shrink-0 items-center gap-3 border-b border-line px-6">
          {selGroup ? (
            <>
              <span className="font-mono text-[14px] text-text">{selGroup.path}</span>
              {selGroup.name && selGroup.name !== selGroup.path.split('/').pop() && (
                <span className="text-[12px] text-muted">{selGroup.name}</span>
              )}
              <Btn
                variant="ghost"
                className="ml-auto h-7"
                onClick={() => setSecretDrawer({ mode: 'add', key: '' })}
              >
                + 新增密钥
              </Btn>
              <Btn variant="danger" className="h-7" onClick={() => setDelGroup(selGroup)}>
                删除分组
              </Btn>
            </>
          ) : (
            <span className="text-[12px] text-muted">{loading ? '加载中…' : '左侧选择或新建分组'}</span>
          )}
        </div>

        <ErrorText>{err}</ErrorText>

        <div className="min-h-0 flex-1 overflow-y-auto">
          {selGroup && secrets.length === 0 && (
            <EmptyState
              title="EMPTY"
              hint="这个分组还没有密钥"
              action={
                <Btn variant="primary" className="mt-3" onClick={() => setSecretDrawer({ mode: 'add', key: '' })}>
                  新增第一条
                </Btn>
              }
            />
          )}
          {secrets.length > 0 && (
            <div className="px-6 py-2">
              <div className="grid grid-cols-[minmax(160px,1fr)_minmax(120px,1.2fr)_110px_112px] gap-4 border-b border-line px-3 pb-2 pt-3">
                <div className="mono-label">KEY</div>
                <div className="mono-label">值</div>
                <div className="mono-label">更新</div>
                <div className="mono-label text-right">操作</div>
              </div>
              {secrets.map((s, i) => {
                const val = revealed[s.key]
                return (
                  <div
                    key={s.id}
                    className="row-in grid grid-cols-[minmax(160px,1fr)_minmax(120px,1.2fr)_110px_112px] items-center gap-4 border-b border-line/60 px-3 hover:bg-surface2/40"
                    style={{ animationDelay: `${i * 30}ms` }}
                  >
                    <div className="flex h-11 items-center overflow-hidden font-mono text-[13px] text-text">
                      {s.key}
                    </div>
                    <div
                      className={cn(
                        'flex h-11 items-center overflow-hidden font-mono text-[12.5px]',
                        val ? 'text-accent/90' : 'text-muted',
                      )}
                    >
                      {val ? (
                        <span className="block truncate" title={val}>
                          {val}
                        </span>
                      ) : (
                        '••••••••'
                      )}
                    </div>
                    <div className="flex h-11 items-center font-mono text-[11px] text-muted">
                      {(s.updated_at ?? '').slice(0, 10)}
                    </div>
                    <div className="flex h-11 items-center justify-end gap-0.5">
                      <button
                        className="h-8 w-8 text-muted transition-colors hover:bg-surface2 hover:text-text"
                        title={val ? '隐藏' : '显示明文'}
                        onClick={() => void toggleReveal(s.key)}
                      >
                        {val ? '⦿' : '👁'}
                      </button>
                      <CopyBtn getText={() => copyText(s.key)} />
                      <button
                        className="h-8 w-8 text-muted transition-colors hover:bg-surface2 hover:text-text"
                        title="修改值"
                        onClick={() => setSecretDrawer({ mode: 'edit', key: s.key })}
                      >
                        ✎
                      </button>
                      <button
                        className="h-8 w-8 text-muted transition-colors hover:bg-danger/10 hover:text-danger"
                        title="删除"
                        onClick={() => setDelSecret(s.key)}
                      >
                        ✕
                      </button>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </div>

      {/* 新建分组 */}
      <NewGroupDialog
        open={newGroupOpen}
        onOpenChange={setNewGroupOpen}
        onCreated={async () => {
          const gs = await loadGroups()
          onChanged()
          const t = buildTree(gs)
          if (t.length > 0) setSel(t[t.length - 1].children.at(-1)?.id ?? t[t.length - 1].id)
        }}
      />

      {/* 新增/修改密钥 */}
      <SecretDrawer
        state={secretDrawer}
        group={selGroup}
        onClose={() => setSecretDrawer(null)}
        onSaved={async () => {
          if (sel != null) await loadSecrets(sel)
          onChanged()
        }}
      />

      {/* 删除分组 */}
      <Confirm
        open={delGroup != null}
        onOpenChange={(v) => !v && setDelGroup(null)}
        busy={busy}
        title="DELETE GROUP"
        onConfirm={async () => {
          if (!delGroup) return
          setBusy(true)
          try {
            await api.deleteGroup(delGroup.id)
            setDelGroup(null)
            const gs = await loadGroups()
            setSel(gs[0]?.id ?? null)
            setSecrets([])
            onChanged()
          } catch (e) {
            setErr(e instanceof ApiError ? e.message : '删除失败')
          } finally {
            setBusy(false)
          }
        }}
      >
        {delGroup && (
          <>
            确定删除分组 <span className="font-mono text-warn">{delGroup.path}</span>？
            {descendants(delGroup).length > 0 && (
              <div className="mt-2 text-muted">
                将连带删除 {descendants(delGroup).length} 个子分组与其中全部密钥。
              </div>
            )}
            <div className="mt-2 text-muted">组内密钥与指向它们的授权会一并删除，不可恢复。</div>
          </>
        )}
      </Confirm>

      {/* 删除密钥 */}
      <Confirm
        open={delSecret != null}
        onOpenChange={(v) => !v && setDelSecret(null)}
        busy={busy}
        title="DELETE SECRET"
        onConfirm={async () => {
          if (!delSecret || sel == null) return
          setBusy(true)
          try {
            await api.deleteSecret(sel, delSecret)
            setDelSecret(null)
            await loadSecrets(sel)
            onChanged()
          } catch (e) {
            setErr(e instanceof ApiError ? e.message : '删除失败')
          } finally {
            setBusy(false)
          }
        }}
      >
        {delSecret && (
          <>
            确定删除 <span className="font-mono text-warn">{delSecret}</span>？指向它的授权会被一并清理，不可恢复。
          </>
        )}
      </Confirm>
    </div>
  )
}

/* ---------- 新建分组 ---------- */

function NewGroupDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onCreated: () => Promise<void> | void
}) {
  const [path, setPath] = useState('')
  const [name, setName] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    setBusy(true)
    setErr('')
    try {
      await api.createGroup(path.trim(), name.trim())
      setPath('')
      setName('')
      onOpenChange(false)
      await onCreated()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal open={open} onOpenChange={onOpenChange} title="NEW GROUP">
      <div className="space-y-4">
        <Field
          label="路径"
          hint="两级：项目/环境，如 minigames/prod。每段小写字母、数字、连字符。"
        >
          <TextInput mono value={path} placeholder="minigames/prod" onChange={(e) => setPath(e.target.value)} />
        </Field>
        <Field label="显示名（可选）">
          <TextInput value={name} placeholder="小游戏 / 生产环境" onChange={(e) => setName(e.target.value)} />
        </Field>
        <ErrorText>{err}</ErrorText>
        <div className="flex justify-end gap-2 pt-1">
          <Btn onClick={() => onOpenChange(false)}>取消</Btn>
          <Btn variant="primary" disabled={busy || !path.trim()} onClick={() => void submit()}>
            {busy ? '创建中…' : '创建'}
          </Btn>
        </div>
      </div>
    </Modal>
  )
}

/* ---------- 新增 / 修改密钥 ---------- */

function SecretDrawer({
  state,
  group,
  onClose,
  onSaved,
}: {
  state: null | { mode: 'add' | 'edit'; key: string }
  group: Group | null
  onClose: () => void
  onSaved: () => Promise<void> | void
}) {
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!state || !group) return
    setErr('')
    if (state.mode === 'add') {
      setKey('')
      setValue('')
    } else {
      setKey(state.key)
      setValue('')
      setLoading(true)
      api
        .reveal(group.id, state.key)
        .then((r) => setValue(r.value as string))
        .catch((e) => setErr(e instanceof ApiError ? e.message : '读取当前值失败'))
        .finally(() => setLoading(false))
    }
  }, [state, group])

  const submit = async () => {
    if (!group || !state) return
    setBusy(true)
    setErr('')
    try {
      if (state.mode === 'add') await api.putSecret(group.id, key.trim(), value)
      else await api.updateSecret(group.id, state.key, value)
      onClose()
      await onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Drawer
      open={state != null && group != null}
      onOpenChange={(v) => !v && onClose()}
      title={state?.mode === 'add' ? 'NEW SECRET' : 'EDIT SECRET'}
    >
      <div className="space-y-4">
        {group && <div className="border border-line bg-bg px-3 py-2 font-mono text-[12px] text-muted">{group.path}</div>}
        <Field label="KEY">
          <TextInput
            mono
            value={key}
            disabled={state?.mode === 'edit'}
            placeholder="OSS_ACCESS_KEY"
            onChange={(e) => setKey(e.target.value)}
          />
        </Field>
        <Field label="值" hint="AES-256-GCM 加密后入库，不会明文落盘。">
          <TextArea
            mono
            rows={6}
            value={value}
            placeholder={loading ? '读取当前值…' : 'value'}
            onChange={(e) => setValue(e.target.value)}
          />
        </Field>
        <ErrorText>{err}</ErrorText>
        <div className="flex justify-end gap-2 pt-2">
          <Btn onClick={onClose}>取消</Btn>
          <Btn variant="primary" disabled={busy || loading || (state?.mode === 'add' ? !key.trim() || !value : !value)} onClick={() => void submit()}>
            {busy ? '保存中…' : '保存'}
          </Btn>
        </div>
      </div>
    </Drawer>
  )
}
