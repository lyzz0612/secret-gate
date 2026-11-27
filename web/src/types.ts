export interface Group {
  id: number
  path: string
  name: string
  created_at: string
}

export interface SecretMeta {
  id: number
  group_id: number
  key: string
  updated_at: string
}

export interface Overview {
  groups: number
  secrets: number
  users: number
  pending: number
  providers: number
}

export interface Status {
  version: string
  schema: number
}
