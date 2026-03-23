import { signal } from "@preact/signals"
import { adminApi, type ScanStatus } from "../lib/api"
import { fetchStats } from "./use-stats"

export const scanStatus = signal<ScanStatus>({ scanning: false, count: 0 })

let pollTimer: ReturnType<typeof setInterval> | null = null

export async function fetchScanStatus() {
  scanStatus.value = await adminApi.scanStatus()
  if (scanStatus.value.scanning && !pollTimer) {
    startPolling()
  }
}

export async function startScan() {
  scanStatus.value = await adminApi.startScan()
  if (!pollTimer) {
    startPolling()
  }
}

function startPolling() {
  pollTimer = setInterval(async () => {
    scanStatus.value = await adminApi.scanStatus()
    if (!scanStatus.value.scanning) {
      stopPolling()
      fetchStats()
    }
  }, 2000)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}
