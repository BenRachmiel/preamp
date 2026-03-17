interface StatCardProps {
  label: string
  value: number | string
  variant?: "default" | "warning"
}

export function StatCard({ label, value, variant = "default" }: StatCardProps) {
  return (
    <div
      class={`rounded-lg border p-6 ${
        variant === "warning"
          ? "bg-warning-subtle border-warning"
          : "bg-card text-card-foreground"
      }`}
    >
      <p class={`text-sm ${variant === "warning" ? "text-warning-foreground" : "text-muted-foreground"}`}>
        {label}
      </p>
      <p class="text-3xl font-bold mt-1">{value}</p>
    </div>
  )
}
