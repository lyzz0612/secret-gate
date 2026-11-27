import type { Grant, PluginType, Provider, Rejection, Rule, UserRow } from './types'

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
  }
}

async function req(path: string, init?: RequestInit): Promise<any> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...init,
    headers: {
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...(init?.headers ?? {}),
    },
  })
  if (res.status === 204) return null
  const text = await res.text()
  let data: any = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = null
    }
  }
  if (!res.ok) {
    throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`)
  }
  return data
}

export const api = {
  me: () => req('/admin/api/me'),
  login: (password: string) =>
    req('/admin/api/login', { method: 'POST', body: JSON.stringify({ password }) }),
  logout: () => req('/admin/api/logout', { method: 'POST' }),

  overview: () => req('/admin/api/overview'),
  status: () => req('/admin/api/status'),

  groups: () => req('/admin/api/groups'),
  createGroup: (path: string, name: string) =>
    req('/admin/api/groups', { method: 'POST', body: JSON.stringify({ path, name }) }),
  deleteGroup: (id: number) => req(`/admin/api/groups/${id}`, { method: 'DELETE' }),

  secrets: (groupId: number) => req(`/admin/api/groups/${groupId}/secrets`),
  putSecret: (groupId: number, key: string, value: string) =>
    req(`/admin/api/groups/${groupId}/secrets`, {
      method: 'POST',
      body: JSON.stringify({ key, value }),
    }),
  updateSecret: (groupId: number, key: string, value: string) =>
    req(`/admin/api/groups/${groupId}/secrets/${encodeURIComponent(key)}`, {
      method: 'PUT',
      body: JSON.stringify({ value }),
    }),
  reveal: (groupId: number, key: string) =>
    req(`/admin/api/groups/${groupId}/secrets/${encodeURIComponent(key)}/reveal`),
  deleteSecret: (groupId: number, key: string) =>
    req(`/admin/api/groups/${groupId}/secrets/${encodeURIComponent(key)}`, { method: 'DELETE' }),

  // ---- M3: 插件与实例 ----
  plugins: () => req('/admin/api/plugins') as Promise<PluginType[]>,
  providers: () => req('/admin/api/providers') as Promise<Provider[]>,
  createProvider: (type: string, name: string, config: Record<string, string>, rules: Rule[]) =>
    req('/admin/api/providers', {
      method: 'POST',
      body: JSON.stringify({ type, name, config, rules }),
    }),
  updateProvider: (id: number, name: string, enabled: boolean, config: Record<string, string>, rules: Rule[]) =>
    req(`/admin/api/providers/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ name, enabled, config, rules }),
    }),
  deleteProvider: (id: number) => req(`/admin/api/providers/${id}`, { method: 'DELETE' }),
  testProvider: (id: number, claims: Record<string, string>) =>
    req(`/admin/api/providers/${id}/test`, { method: 'POST', body: JSON.stringify({ claims }) }),

  // ---- M3: 用户与授权 ----
  users: (status?: string) => req(`/admin/api/users${status ? `?status=${status}` : ''}`) as Promise<UserRow[]>,
  userDetail: (id: number) => req(`/admin/api/users/${id}`),
  approveUser: (id: number) => req(`/admin/api/users/${id}/approve`, { method: 'POST', body: '{}' }),
  deleteUser: (id: number) => req(`/admin/api/users/${id}`, { method: 'DELETE' }),
  userGrants: (id: number) => req(`/admin/api/users/${id}/grants`) as Promise<Grant[]>,
  grants: (subjectKind?: string, subjectId?: number) =>
    req(`/admin/api/grants${subjectKind ? `?subject_kind=${subjectKind}&subject_id=${subjectId}` : ''}`) as Promise<Grant[]>,
  createGrant: (subjectKind: string, subjectId: number, targetKind: string, targetId: number) =>
    req('/admin/api/grants', {
      method: 'POST',
      body: JSON.stringify({ subject_kind: subjectKind, subject_id: subjectId, target_kind: targetKind, target_id: targetId }),
    }),
  deleteGrant: (id: number) => req(`/admin/api/grants/${id}`, { method: 'DELETE' }),
  rejections: () => req('/admin/api/rejections') as Promise<Rejection[]>,

  // ---- M4: 用户组 ----
  userGroups: () => req('/admin/api/user-groups'),
  createUserGroup: (name: string, description: string) =>
    req('/admin/api/user-groups', { method: 'POST', body: JSON.stringify({ name, description }) }),
  deleteUserGroup: (id: number) => req(`/admin/api/user-groups/${id}`, { method: 'DELETE' }),
  userGroupMembers: (id: number) => req(`/admin/api/user-groups/${id}/members`) as Promise<UserRow[]>,
  setUserGroupMembers: (id: number, userIds: number[]) =>
    req(`/admin/api/user-groups/${id}/members`, { method: 'PUT', body: JSON.stringify({ user_ids: userIds }) }),
}