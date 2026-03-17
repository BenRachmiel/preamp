import type { ComponentChildren } from "preact"
import { route, navigate } from "../lib/router"
import { config } from "../lib/config"

interface Props {
  username: string
  children: ComponentChildren
}

const tabs = [
  { path: "/credentials", label: "Credentials" },
  { path: "/library", label: "Library" },
]

export function Layout({ username, children }: Props) {
  return (
    <div class="min-h-screen">
      <header class="border-b">
        <div class="mx-auto max-w-4xl flex items-center justify-between px-4 py-3">
          <div class="flex items-center gap-6">
            <h1 class="text-lg font-semibold tracking-tight">preamp</h1>
            <nav class="flex gap-1">
              {tabs.map((tab) => (
                <a
                  key={tab.path}
                  href={`#${tab.path}`}
                  onClick={(e) => {
                    e.preventDefault()
                    navigate(tab.path)
                  }}
                  class={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                    route.value === tab.path
                      ? "bg-accent text-accent-foreground"
                      : "text-muted-foreground hover:text-foreground nav-link"
                  }`}
                >
                  {tab.label}
                </a>
              ))}
            </nav>
          </div>
          <div class="flex items-center gap-2">
            <span class="text-sm text-muted-foreground">{username}</span>
            {!config.noAuth && (
              <a
                href="/oauth2/sign_out"
                class="rounded-md px-2.5 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                Sign out
              </a>
            )}
          </div>
        </div>
      </header>
      <main class="mx-auto max-w-4xl px-4 py-6">
        {children}
      </main>
    </div>
  )
}
