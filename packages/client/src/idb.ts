// IndexedDB message cache — cold-start acceleration.
// Stores messages locally so SSE only ships the delta on reload.
// All operations are fire-and-forget or async; never blocks the render path.

const DB_NAME    = 'parlay-chat'
const DB_VERSION = 1
const STORE      = 'messages'
const MAX_STORED = 1000   // prune to this after each batch write

let _db: IDBDatabase | null = null

function openDb(): Promise<IDBDatabase> {
  if (_db) return Promise.resolve(_db)
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION)
    req.onupgradeneeded = () => {
      const db = req.result
      if (!db.objectStoreNames.contains(STORE)) {
        const s = db.createObjectStore(STORE, { keyPath: 'id' })
        s.createIndex('ts', 'ts', { unique: false })
      }
    }
    req.onsuccess = () => { _db = req.result; resolve(_db) }
    req.onerror   = () => reject(req.error)
  })
}

// Returns all cached messages sorted by ts, plus the id of the newest one.
// Called at startup; ~5-20ms on a warm IDB with 1000 entries.
export async function loadStored(): Promise<{ msgs: any[]; lastId: string | undefined }> {
  try {
    const db = await openDb()
    return new Promise((resolve) => {
      const tx  = db.transaction(STORE, 'readonly')
      const req = tx.objectStore(STORE).index('ts').getAll()
      req.onsuccess = () => {
        const sorted = (req.result as any[]).sort(
          (a, b) => new Date(a.ts).getTime() - new Date(b.ts).getTime()
        )
        resolve({ msgs: sorted, lastId: sorted.at(-1)?.id })
      }
      req.onerror = () => resolve({ msgs: [], lastId: undefined })
    })
  } catch {
    return { msgs: [], lastId: undefined }
  }
}

// Upserts a batch of messages then prunes to MAX_STORED.
// Fire-and-forget — callers don't await this.
export async function storeMessages(incoming: any[]): Promise<void> {
  if (!incoming.length) return
  try {
    const db = await openDb()
    await new Promise<void>((resolve, reject) => {
      const tx    = db.transaction(STORE, 'readwrite')
      const store = tx.objectStore(STORE)
      for (const m of incoming) store.put(m)
      tx.oncomplete = () => resolve()
      tx.onerror    = () => reject(tx.error)
    })
    await pruneOld(db)
  } catch {}
}

// Deletes oldest messages (by ts index order) until count ≤ MAX_STORED.
async function pruneOld(db: IDBDatabase): Promise<void> {
  return new Promise((resolve) => {
    const tx    = db.transaction(STORE, 'readwrite')
    const store = tx.objectStore(STORE)
    const countReq = store.count()
    countReq.onsuccess = () => {
      const excess = countReq.result - MAX_STORED
      if (excess <= 0) { resolve(); return }
      const cursor = store.index('ts').openCursor()
      let deleted  = 0
      cursor.onsuccess = () => {
        const c = cursor.result
        if (!c || deleted >= excess) { resolve(); return }
        c.delete(); deleted++; c.continue()
      }
      cursor.onerror = () => resolve()
    }
    countReq.onerror = () => resolve()
  })
}
