// ── IndexedDB clip cache for server TTS ──────────────────────────────────────
// WAV blobs keyed by hash(voice|text), LRU-capped. Makes tap-to-replay instant
// and survives page reloads (unlike the server's in-memory 40-clip cache).

const DB_NAME = 'pa-tts-cache'
const STORE = 'clips'
const MAX_CLIPS = 200
const MAX_BYTES = 50 * 1024 * 1024

// FNV-1a string hash — crypto.subtle is unavailable on non-secure origins
// (the panel is reached over plain http via LAN/Tailscale IPs).
export function clipKey(text: string, voice = 'default'): string {
  const s = `${voice}|${text}`
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 0x01000193) }
  return (h >>> 0).toString(36) + '-' + s.length
}

function reqP<T>(req: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
}

let _db: Promise<IDBDatabase> | null = null
function db(): Promise<IDBDatabase> {
  if (_db) return _db
  _db = new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 1)
    req.onupgradeneeded = () => {
      const store = req.result.createObjectStore(STORE, { keyPath: 'key' })
      store.createIndex('ts', 'ts')
    }
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
  return _db
}

interface ClipRec { key: string; blob: Blob; ts: number; size: number }

export async function cacheGet(key: string): Promise<Blob | null> {
  try {
    const d = await db()
    const rec = await reqP<ClipRec | undefined>(d.transaction(STORE).objectStore(STORE).get(key))
    if (!rec) return null
    rec.ts = Date.now()   // LRU touch
    d.transaction(STORE, 'readwrite').objectStore(STORE).put(rec)
    return rec.blob
  } catch { return null }
}

export async function cachePut(key: string, blob: Blob): Promise<void> {
  try {
    const d = await db()
    const rec: ClipRec = { key, blob, ts: Date.now(), size: blob.size }
    await reqP(d.transaction(STORE, 'readwrite').objectStore(STORE).put(rec))
    await prune(d)
  } catch { /* cache is best-effort */ }
}

async function prune(d: IDBDatabase): Promise<void> {
  const all = await reqP<ClipRec[]>(d.transaction(STORE).objectStore(STORE).getAll())
  let bytes = all.reduce((n, r) => n + r.size, 0)
  if (all.length <= MAX_CLIPS && bytes <= MAX_BYTES) return
  all.sort((a, b) => a.ts - b.ts)   // oldest first
  const store = d.transaction(STORE, 'readwrite').objectStore(STORE)
  let count = all.length
  for (const r of all) {
    if (count <= MAX_CLIPS && bytes <= MAX_BYTES) break
    store.delete(r.key)
    count--; bytes -= r.size
  }
}

export async function cacheStats(): Promise<{ clips: number; bytes: number }> {
  try {
    const d = await db()
    const all = await reqP<ClipRec[]>(d.transaction(STORE).objectStore(STORE).getAll())
    return { clips: all.length, bytes: all.reduce((n, r) => n + r.size, 0) }
  } catch { return { clips: 0, bytes: 0 } }
}
