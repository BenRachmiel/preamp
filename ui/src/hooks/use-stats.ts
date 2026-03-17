import { signal } from "@preact/signals"
import { adminApi, type Stats } from "../lib/api"

export const stats = signal<Stats | null>(null)

export async function fetchStats() {
  stats.value = await adminApi.stats()
}
