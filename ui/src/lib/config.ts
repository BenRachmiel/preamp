interface PreampConfig {
  apiUrl: string
  noAuth: boolean
  devUsername: string
}

const env = (window as any).__PREAMP_CONFIG__ ?? {}

export const config: PreampConfig = {
  apiUrl: env.apiUrl ?? import.meta.env.VITE_API_URL ?? "",
  noAuth: env.noAuth ?? import.meta.env.VITE_NO_AUTH === "1",
  devUsername: env.devUsername ?? import.meta.env.VITE_DEV_USERNAME ?? "dev",
}
