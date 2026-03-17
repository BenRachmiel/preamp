import { config } from "./config"

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

async function api<T = any>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${config.apiUrl}${path}`, init)
  if (res.status === 401) {
    if (!config.noAuth) window.location.href = "/oauth2/sign_in"
    throw new ApiError(401, "unauthorized")
  }
  if (!res.ok) throw new ApiError(res.status, await res.text())
  if (res.status === 204) return null as T
  return res.json()
}

export interface Credential {
  id: string
  username: string
  client_name: string
  legacy_auth: boolean
  created_at: string
  expires_at: string
  expired: boolean
  secret?: string
}

export interface Stats {
  artists: number
  albums: number
  songs: number
  albums_missing_art: number
  songs_unknown_artist: number
  songs_no_genre: number
  songs_no_year: number
  songs_zero_duration: number
  albums_no_year: number
  albums_no_genre: number
}

export interface ScanStatus {
  scanning: boolean
  count: number
}

export const adminApi = {
  whoami: () => api<{ username: string }>("/admin/whoami"),
  listCredentials: () => api<Credential[]>("/admin/credentials"),
  createCredential: (body: { client_name: string; legacy_auth: boolean; ttl: string }) =>
    api<Credential>("/admin/credentials", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  renewCredential: (id: string) =>
    api<Credential>(`/admin/credentials/${id}/renew`, { method: "POST" }),
  deleteCredential: (id: string) =>
    api<null>(`/admin/credentials/${id}`, { method: "DELETE" }),
  stats: () => api<Stats>("/admin/stats"),
  scanStatus: () => api<ScanStatus>("/admin/scan"),
  startScan: () => api<ScanStatus>("/admin/scan", { method: "POST" }),
}
