"use client";

import { useEffect, useRef, useState } from "react";

// The Go server's base URL. NEXT_PUBLIC_ vars are inlined at build time and
// readable in the browser, which is what a client component needs here.
const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

const POLL_INTERVAL_MS = 1000;

// Shape of one client's entry in the GET /stats response — cumulative
// counters since the server started (or since Redis last expired them),
// never reset on every poll.
type ClientStats = { allowed: number; denied: number };
type StatsResponse = Record<string, ClientStats>;

// One row of the rendered table/grid: a client's totals plus the per-second
// rates derived from them (see the diffing comment in the effect below).
type ClientRow = {
  id: string;
  allowed: number;
  denied: number;
  allowRate: number;
  denyRate: number;
};

export default function Home() {
  const [current, setCurrent] = useState<StatsResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  // The previous poll's snapshot, kept in a ref rather than state: updating
  // it should never itself trigger a re-render — only a fresh `current`
  // should. It's what makes rate computation possible at all, since /stats
  // only ever reports cumulative totals, never a rate.
  const previousRef = useRef<StatsResponse | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function poll() {
      try {
        const res = await fetch(`${API_URL}/stats`, { cache: "no-store" });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data: StatsResponse = await res.json();
        if (cancelled) return;

        // The core idea: /stats gives cumulative counts, not rates. To get
        // a live "requests/sec" figure, diff this poll's counts against the
        // PREVIOUS poll's counts — at a fixed ~1s poll interval, that
        // difference IS approximately the per-second rate. So before
        // replacing `current` with the freshly fetched data, stash the
        // snapshot we're about to replace into previousRef — the render
        // right after this uses the new data as "current" and the just-
        // replaced data as "previous", giving exactly one interval's worth
        // of delta to diff.
        setCurrent((prevCurrent) => {
          previousRef.current = prevCurrent;
          return data;
        });
        setError(null);
      } catch (err) {
        if (cancelled) return;
        // Report the error but deliberately leave `current`/previousRef
        // untouched: keep showing the last known-good snapshot instead of
        // blanking the screen, and keep the interval running so the
        // dashboard recovers on its own once the API is reachable again.
        setError(err instanceof Error ? err.message : String(err));
      }
    }

    poll(); // fetch immediately, rather than waiting a full interval first
    const id = setInterval(poll, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  const previous = previousRef.current;

  const rows: ClientRow[] = current
    ? Object.entries(current)
        .map(([id, stats]) => {
          const prevStats = previous?.[id];
          // Guard against no previous snapshot yet (first poll ever, or a
          // client that just showed up for the first time): rate is simply
          // 0, never NaN or undefined. Math.max(0, ...) also guards against
          // a negative delta if the counters were ever reset underneath us
          // (e.g. a server restart, or a Redis stats key expiring).
          const allowRate = prevStats ? Math.max(0, stats.allowed - prevStats.allowed) : 0;
          const denyRate = prevStats ? Math.max(0, stats.denied - prevStats.denied) : 0;
          return { id, allowed: stats.allowed, denied: stats.denied, allowRate, denyRate };
        })
        // Sort by client ID so rows hold a stable order across polls,
        // instead of jumping around as counts change.
        .sort((a, b) => a.id.localeCompare(b.id))
    : [];

  return (
    <main className="mx-auto max-w-4xl p-8">
      <h1 className="mb-1 text-2xl font-semibold">Rate Limiter Dashboard</h1>
      <p className="mb-6 text-sm text-gray-400">
        Polling <code className="rounded bg-gray-900 px-1 py-0.5">{API_URL}/stats</code> every
        second
      </p>

      {error && (
        <div className="mb-6 rounded-md border border-red-800 bg-red-950 px-4 py-2 text-sm text-red-200">
          Can&apos;t reach API at {API_URL} — retrying… ({error})
        </div>
      )}

      {rows.length === 0 && !error && (
        <p className="text-sm text-gray-400">
          No clients yet — send some traffic to{" "}
          <code className="rounded bg-gray-900 px-1 py-0.5">/check/{"{key}"}</code>.
        </p>
      )}

      {rows.length > 0 && (
        <div className="grid gap-3">
          {rows.map((row) => (
            <ClientCard key={row.id} row={row} />
          ))}
        </div>
      )}
    </main>
  );
}

function ClientCard({ row }: { row: ClientRow }) {
  const totalRate = row.allowRate + row.denyRate;
  // Bar widths are proportional to the RATES (this interval's traffic
  // split), not the lifetime totals — that's what makes a deny spike
  // visible the moment a client starts getting throttled, rather than
  // being drowned out by however much history it's accumulated. With no
  // traffic this interval, split evenly instead of dividing by zero.
  const allowPct = totalRate > 0 ? (row.allowRate / totalRate) * 100 : 50;
  const denyPct = totalRate > 0 ? (row.denyRate / totalRate) * 100 : 50;

  return (
    <div className="rounded-lg border border-gray-800 bg-gray-900 p-4">
      <div className="mb-2 flex items-baseline justify-between">
        <span className="font-mono text-sm">{row.id}</span>
        <span className="text-xs text-gray-400">
          {row.allowRate}/s allow &middot; {row.denyRate}/s deny
        </span>
      </div>

      <div className="mb-2 flex justify-between text-sm text-gray-300">
        <span>Allowed: {row.allowed}</span>
        <span>Denied: {row.denied}</span>
      </div>

      <div className="flex h-2 w-full overflow-hidden rounded-full bg-gray-800">
        <div className="h-full bg-green-500" style={{ width: `${allowPct}%` }} />
        <div className="h-full bg-red-500" style={{ width: `${denyPct}%` }} />
      </div>
    </div>
  );
}
