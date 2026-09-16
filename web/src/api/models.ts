export interface LoginResponse {
  access_token: string
  refresh_token: string
  token_type: string
}

export interface LoginRequest {
  email: string
  password: string
}

export interface MeResponse {
  id: string
  username: string
  email: string
}