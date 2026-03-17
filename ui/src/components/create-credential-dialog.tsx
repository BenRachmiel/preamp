import { signal } from "@preact/signals"
import { useRef } from "preact/hooks"
import type { Credential } from "../lib/api"
import { createCredential } from "../hooks/use-credentials"

const result = signal<Credential | null>(null)
const error = signal("")
const submitting = signal(false)

interface Props {
  dialogRef: (el: HTMLDialogElement | null) => void
}

export function CreateCredentialDialog({ dialogRef }: Props) {
  const formRef = useRef<HTMLFormElement>(null)
  const innerRef = useRef<HTMLDialogElement>(null)

  function setRef(el: HTMLDialogElement | null) {
    innerRef.current = el
    dialogRef(el)
  }

  function handleClose() {
    result.value = null
    error.value = ""
    formRef.current?.reset()
  }

  async function handleSubmit(e: Event) {
    e.preventDefault()
    const form = formRef.current!
    const data = new FormData(form)
    submitting.value = true
    error.value = ""
    try {
      result.value = await createCredential({
        client_name: data.get("client_name") as string,
        legacy_auth: data.has("legacy_auth"),
        ttl: data.get("ttl") as string,
      })
    } catch (err: any) {
      error.value = err.message || "Failed to create credential"
    } finally {
      submitting.value = false
    }
  }

  function handleCopy() {
    if (result.value?.secret) {
      navigator.clipboard.writeText(result.value.secret)
    }
  }

  return (
    <dialog
      ref={setRef}
      onClose={handleClose}
      class="rounded-lg border bg-background text-foreground p-0 w-full max-w-md dialog-shadow"
    >
      {result.value ? (
        <div class="flex flex-col gap-4 p-6">
          <h2 class="text-lg font-semibold">Credential Created</h2>
          <p class="text-sm text-muted-foreground">
            Copy the secret below. It will not be shown again.
          </p>
          <div class="flex items-center gap-2">
            <code class="flex-1 rounded-md bg-muted px-3 py-2 text-sm font-mono break-all select-all">
              {result.value.secret}
            </code>
            <button type="button" onClick={handleCopy} class="btn btn-primary">
              Copy
            </button>
          </div>
          <p class="text-xs text-warning-foreground bg-warning-subtle rounded-md px-3 py-2">
            This secret will not be shown again. Store it securely.
          </p>
          <div class="flex justify-end">
            <button type="button" onClick={() => innerRef.current?.close()} class="btn btn-secondary">
              Done
            </button>
          </div>
        </div>
      ) : (
        <form ref={formRef} onSubmit={handleSubmit} class="flex flex-col gap-4 p-6">
          <h2 class="text-lg font-semibold">New Credential</h2>

          {error.value && (
            <p class="text-sm text-destructive">{error.value}</p>
          )}

          <div class="flex flex-col gap-2">
            <label class="text-sm font-medium" for="client_name">Client Name</label>
            <input
              id="client_name"
              name="client_name"
              type="text"
              required
              placeholder="e.g. Symfonium, Feishin"
              class="w-full rounded-md border bg-background px-3 py-2 text-sm input-ring"
            />
          </div>

          <div class="flex items-center justify-between">
            <div>
              <label class="text-sm font-medium" for="legacy_auth">Legacy Auth</label>
              <p class="text-xs text-muted-foreground">Enable for clients using password auth (most clients)</p>
            </div>
            <input id="legacy_auth" name="legacy_auth" type="checkbox" checked />
          </div>

          <div class="flex flex-col gap-2">
            <label class="text-sm font-medium" for="ttl">Expires After</label>
            <select
              id="ttl"
              name="ttl"
              class="w-full rounded-md border bg-background px-3 py-2 text-sm input-ring"
            >
              <option value="1h">1 hour</option>
              <option value="24h">24 hours</option>
              <option value="168h" selected>7 days</option>
              <option value="720h">30 days</option>
              <option value="0">Never</option>
            </select>
          </div>

          <div class="flex justify-end gap-2 pt-2">
            <button type="button" onClick={() => innerRef.current?.close()} class="btn btn-secondary">
              Cancel
            </button>
            <button type="submit" disabled={submitting.value} class="btn btn-primary">
              {submitting.value ? "Creating..." : "Create"}
            </button>
          </div>
        </form>
      )}
    </dialog>
  )
}
