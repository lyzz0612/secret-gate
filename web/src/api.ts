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
}
