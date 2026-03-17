import type { Credential } from "../lib/api"
import { relativeTime } from "../lib/time"

interface Props {
  credentials: Credential[]
  onRenew: (id: string) => void
  onRevoke: (id: string) => void
}

export function CredentialTable({ credentials, onRenew, onRevoke }: Props) {
  if (credentials.length === 0) {
    return (
      <div class="rounded-lg border p-8 text-center text-muted-foreground">
        No credentials yet. Create one to connect a Subsonic client.
      </div>
    )
  }

  return (
    <div class="rounded-lg border overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b bg-muted-half">
            <th>Client</th>
            <th>Type</th>
            <th>Created</th>
            <th>Expires</th>
            <th class="text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          {credentials.map((c) => (
            <tr key={c.id} class="border-b last:border-b-0">
              <td class="font-medium">{c.client_name}</td>
              <td>
                <span class={`inline-flex items-center rounded-md px-2 py-1 text-xs font-medium ${c.legacy_auth ? "badge-legacy" : "badge-apikey"}`}>
                  {c.legacy_auth ? "Legacy" : "API Key"}
                </span>
              </td>
              <td class="text-muted-foreground">{relativeTime(c.created_at)}</td>
              <td>
                {c.expires_at ? (
                  <span class={c.expired ? "text-destructive font-medium" : "text-muted-foreground"}>
                    {c.expired ? "Expired" : relativeTime(c.expires_at)}
                  </span>
                ) : (
                  <span class="text-muted-foreground">Never</span>
                )}
              </td>
              <td class="text-right">
                <div class="inline-flex gap-2">
                  {c.expires_at && (
                    <button type="button" onClick={() => onRenew(c.id)} class="btn btn-secondary btn-sm">
                      Renew
                    </button>
                  )}
                  <button type="button" onClick={() => onRevoke(c.id)} class="btn btn-ghost-danger btn-sm">
                    Revoke
                  </button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
