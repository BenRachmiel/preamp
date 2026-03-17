import { signal } from "@preact/signals"

export const route = signal(location.hash.slice(1) || "/credentials")

window.addEventListener("hashchange", () => {
  route.value = location.hash.slice(1) || "/credentials"
})

export function navigate(path: string) {
  location.hash = path
}
