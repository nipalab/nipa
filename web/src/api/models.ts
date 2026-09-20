export interface LoginRequest {
  email: string
  password: string
}

export interface TokenResponse {
  access_token: string
  token_type: string
  expires_in: number
}

export interface MeResponse {
  id: string
  name: string
  email: string
  photo_url: string
  is_admin: boolean
  is_super_admin: boolean
}

export interface PermissionEntry {
  path_prefix: string
  permission: number
}

export interface ProjectPermissionResponse {
  project_permission: number
  rules: PermissionEntry[]
  defaults: PermissionEntry[]
}
