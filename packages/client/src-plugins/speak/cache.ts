// ── Speak plugin: clip cache (IndexedDB LRU) + fetch ────────────────────────
// Self-contained — plugin bundles must not import core modules.

const CHAT_BASE = '/api/chat'
const DB_NAME = 'pa-tts-cache'
const STORE = 'clips'
const MAX_CLIPS = 200
const MAX_BYTES = 50 * 1024 * 1024

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
    rec.ts = Date.now()
    d.transaction(STORE, 'readwrite').objectStore(STORE).put(rec)
    return rec.blob
  } catch { return null }
}

export async function cacheHas(key: string): Promise<boolean> {
  try {
    const d = await db()
    return !!(await reqP(d.transaction(STORE).objectStore(STORE).getKey(key)))
  } catch { return false }
}

async function cachePut(key: string, blob: Blob): Promise<void> {
  try {
    const d = await db()
    await reqP(d.transaction(STORE, 'readwrite').objectStore(STORE).put({ key, blob, ts: Date.now(), size: blob.size }))
    const all = await reqP<ClipRec[]>(d.transaction(STORE).objectStore(STORE).getAll())
    let bytes = all.reduce((n, r) => n + r.size, 0)
    if (all.length <= MAX_CLIPS && bytes <= MAX_BYTES) return
    all.sort((a, b) => a.ts - b.ts)
    const store = d.transaction(STORE, 'readwrite').objectStore(STORE)
    let count = all.length
    for (const r of all) {
      if (count <= MAX_CLIPS && bytes <= MAX_BYTES) break
      store.delete(r.key); count--; bytes -= r.size
    }
  } catch { /* best-effort */ }
}

export async function cacheStats(): Promise<{ clips: number; bytes: number }> {
  try {
    const d = await db()
    const all = await reqP<ClipRec[]>(d.transaction(STORE).objectStore(STORE).getAll())
    return { clips: all.length, bytes: all.reduce((n, r) => n + r.size, 0) }
  } catch { return { clips: 0, bytes: 0 } }
}

let fetchCount = 0
export function clipFetches(): number { return fetchCount }

function isRiff(buf: ArrayBuffer): boolean {
  const h = new Uint8Array(buf.slice(0, 4))
  return h[0] === 0x52 && h[1] === 0x49 && h[2] === 0x46 && h[3] === 0x46
}

export async function getClip(text: string): Promise<Blob | null> {
  const key = clipKey(text)
  const hit = await cacheGet(key)
  if (hit) return hit
  try {
    fetchCount++
    const r = await fetch(`${CHAT_BASE}/tts`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text }),
    })
    const buf = await r.arrayBuffer()
    if (!isRiff(buf)) return null
    const blob = new Blob([buf], { type: 'audio/wav' })
    void cachePut(key, blob)
    return blob
  } catch { return null }
}
