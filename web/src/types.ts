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

export interface PluginField {
  key: string
  label: string
  help?: string
  type: string
  options?: string[]
  required?: boolean
  secret?: boolean
  placeholder?: string
}

export interface ClaimField {
  key: string
  label: string
  example?: string
}

export interface PluginType {
  type: string
  label: string
  config_fields: PluginField[]
  claim_fields: ClaimField[]
}

export interface Provider {
  id: number
  type: string
  name: string
  enabled: boolean
  config_json: string
  rules_json: string
  created_at: string
  updated_at: string
  type_label: string
}

export interface Rule {
  field: string
  pattern: string
}

export interface UserRow {
  id: number
  provider_type: string
  external_id: string
  display: string
  kind: string
  status: string
  source_id: number
  created_at: string
  last_seen_at: string
}

export interface Grant {
  id: number
  subject_kind: string
  subject_id: number
  target_kind: string
  target_id: number
  created_at: string
}

export interface Rejection {
  ts: string
  type: string
  ext_id: string
  display: string
  instance: string
  reason: string
  source_ip: string
}
