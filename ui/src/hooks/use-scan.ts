import { signal } from "@preact/signals"
import { adminApi, type ScanStatus } from "../lib/api"

export const scanStatus = signal<ScanStatus>({ scanning: false, count: 0 })

let pollTimer: ReturnType<typeof setInterval> | null = null

export async function fetchScanStatus() {
  scanStatus.value = await adminApi.scanStatus()
  if (scanStatus.value.scanning && !pollTimer) {
    pollTimer = setInterval(async () => {
      scanStatus.value = await adminApi.scanStatus()
      if (!scanStatus.value.scanning) stopPolling()
    }, 2000)
  }
}

export async function startScan() {
  scanStatus.value = await adminApi.startScan()
  if (!pollTimer) {
    pollTimer = setInterval(async () => {
      scanStatus.value = await adminApi.scanStatus()
      if (!scanStatus.value.scanning) stopPolling()
    }, 2000)
  }
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}
