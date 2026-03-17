import { useEffect, useRef } from "preact/hooks"
import {
  credentials,
  credentialsLoading,
  fetchCredentials,
  renewCredential,
  deleteCredential,
} from "../hooks/use-credentials"
import { CredentialTable } from "../components/credential-table"
import { CreateCredentialDialog } from "../components/create-credential-dialog"

export function CredentialsPage() {
  const dialogRef = useRef<HTMLDialogElement | null>(null)

  useEffect(() => {
    fetchCredentials()
  }, [])

  function handleRenew(id: string) {
    renewCredential(id)
  }

  function handleRevoke(id: string) {
    if (window.confirm("Revoke this credential? Connected clients will lose access.")) {
      deleteCredential(id)
    }
  }

  return (
    <div class="flex flex-col gap-4">
      <div class="flex items-center justify-between">
        <div>
          <h2 class="text-xl font-semibold tracking-tight">Credentials</h2>
          <p class="text-sm text-muted-foreground">
            Manage Subsonic client credentials
          </p>
        </div>
        <button
          type="button"
          onClick={() => dialogRef.current?.showModal()}
          class="btn btn-primary"
        >
          New Credential
        </button>
      </div>

      {credentialsLoading.value ? (
        <div class="rounded-lg border p-8 text-center text-muted-foreground">
          Loading...
        </div>
      ) : (
        <CredentialTable
          credentials={credentials.value}
          onRenew={handleRenew}
          onRevoke={handleRevoke}
        />
      )}

      <CreateCredentialDialog dialogRef={(el) => (dialogRef.current = el)} />
    </div>
  )
}
