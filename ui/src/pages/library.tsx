import { useEffect } from "preact/hooks"
import { stats, fetchStats } from "../hooks/use-stats"
import { scanStatus, fetchScanStatus, startScan } from "../hooks/use-scan"
import { StatCard } from "../components/stat-card"

interface Issue {
  label: string
  count: number
}

export function LibraryPage() {
  useEffect(() => {
    fetchStats()
    fetchScanStatus()
  }, [])

  const s = stats.value
  const scan = scanStatus.value

  const issues: Issue[] = s
    ? [
        { label: "Albums missing cover art", count: s.albums_missing_art },
        { label: "Albums missing year", count: s.albums_no_year },
        { label: "Albums missing genre", count: s.albums_no_genre },
        { label: "Songs by Unknown Artist", count: s.songs_unknown_artist },
        { label: "Songs missing genre", count: s.songs_no_genre },
        { label: "Songs missing year", count: s.songs_no_year },
        { label: "Songs with zero duration", count: s.songs_zero_duration },
      ].filter((i) => i.count > 0)
    : []

  return (
    <div class="flex flex-col gap-6">
      <div>
        <h2 class="text-xl font-semibold tracking-tight">Library</h2>
        <p class="text-sm text-muted-foreground">Music library statistics and scanning</p>
      </div>

      {s && (
        <>
          <div class="grid grid-cols-3 gap-4">
            <StatCard label="Artists" value={s.artists} />
            <StatCard label="Albums" value={s.albums} />
            <StatCard label="Songs" value={s.songs} />
          </div>

          {issues.length > 0 && (
            <div class="flex flex-col gap-2">
              <h3 class="text-sm font-medium text-muted-foreground">Data Quality</h3>
              <div class="grid grid-cols-2 gap-3">
                {issues.map((i) => (
                  <StatCard key={i.label} label={i.label} value={i.count} variant="warning" />
                ))}
              </div>
            </div>
          )}
        </>
      )}

      <div class="flex flex-col gap-4 rounded-lg border p-6 bg-card text-card-foreground">
        <div class="flex items-center justify-between">
          <div>
            <h3 class="font-medium">Library Scan</h3>
            <p class="text-sm text-muted-foreground">
              {scan.scanning
                ? `Scanning... ${scan.count} tracks found`
                : `Last scan: ${scan.count} tracks indexed`}
            </p>
          </div>
          <button
            type="button"
            disabled={scan.scanning}
            onClick={() => startScan()}
            class="btn btn-primary"
          >
            {scan.scanning ? "Scanning..." : "Rescan Library"}
          </button>
        </div>
        {scan.scanning && (
          <div class="h-1.5 rounded-full bg-muted overflow-hidden">
            <div class="h-full bg-primary rounded-full animate-pulse w-2/3" />
          </div>
        )}
      </div>
    </div>
  )
}
