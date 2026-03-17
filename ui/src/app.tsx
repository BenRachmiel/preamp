import { signal } from "@preact/signals"
import { useEffect } from "preact/hooks"
import { route, navigate } from "./lib/router"
import { adminApi, ApiError } from "./lib/api"
import { config } from "./lib/config"
import { Layout } from "./components/layout"
import { CredentialsPage } from "./pages/credentials"
import { LibraryPage } from "./pages/library"

const username = signal("")
const authChecked = signal(false)

export function App() {
  useEffect(() => {
    adminApi.whoami().then(
      (data) => {
        username.value = data.username
        authChecked.value = true
      },
      (err) => {
        if (err instanceof ApiError && err.status === 401 && !config.noAuth) {
          window.location.href = "/oauth2/sign_in"
          return
        }
        // In noAuth mode, fall back to dev username
        username.value = config.devUsername
        authChecked.value = true
      },
    )
  }, [])

  if (!authChecked.value) {
    return (
      <div class="min-h-screen flex items-center justify-center text-muted-foreground">
        Loading...
      </div>
    )
  }

  const page = route.value
  if (page !== "/credentials" && page !== "/library") {
    navigate("/credentials")
  }

  return (
    <Layout username={username.value}>
      {page === "/library" ? <LibraryPage /> : <CredentialsPage />}
    </Layout>
  )
}
