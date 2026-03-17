import { signal } from "@preact/signals"
import { adminApi, type Credential } from "../lib/api"

export const credentials = signal<Credential[]>([])
export const credentialsLoading = signal(false)

export async function fetchCredentials() {
  credentialsLoading.value = true
  try {
    credentials.value = await adminApi.listCredentials()
  } finally {
    credentialsLoading.value = false
  }
}

export async function createCredential(body: {
  client_name: string
  legacy_auth: boolean
  ttl: string
}) {
  const result = await adminApi.createCredential(body)
  await fetchCredentials()
  return result
}

export async function renewCredential(id: string) {
  await adminApi.renewCredential(id)
  await fetchCredentials()
}

export async function deleteCredential(id: string) {
  await adminApi.deleteCredential(id)
  await fetchCredentials()
}
