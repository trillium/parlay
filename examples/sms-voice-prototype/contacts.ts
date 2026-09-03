// contacts.ts — macOS AddressBook identifier→name resolution for the
// SMS voice-flow prototype. `imsg` exposes no contact lookup of its own, so
// the server resolves bare-number chats itself, straight from the AddressBook
// sqlite databases Contacts.app maintains.
//
// Reads every `~/Library/Application Support/AddressBook/Sources/*/AddressBook-v22.abcddb`
// read-only via bun:sqlite (built into Bun, no deps). Builds an in-memory
// phone/email → name map once at startup and caches it for the process
// lifetime. A failed or missing AddressBook read degrades gracefully to
// returning null (callers fall back to the raw identifier) — this must never
// throw, since not every machine running this prototype has Contacts data or
// Full Disk Access.

import { Database } from 'bun:sqlite';
import { existsSync, readdirSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

export interface ContactRecord {
  firstName: string | null;
  lastName: string | null;
  organization: string | null;
}

// key: normalized phone (last 10 digits) or lowercased email
export type ContactIndex = Map<string, ContactRecord>;

// --- pure helpers (unit-testable without touching a real DB) ---

// US-default normalization: strip non-digits, compare on the last 10 digits.
export function normalizePhoneKey(raw: string): string | null {
  const digits = raw.replace(/\D/g, '');
  if (digits.length < 10) return null;
  return digits.slice(-10);
}

export function normalizeEmailKey(raw: string): string | null {
  const trimmed = raw.trim().toLowerCase();
  return trimmed || null;
}

export function identifierKey(identifier: string): string | null {
  return identifier.includes('@') ? normalizeEmailKey(identifier) : normalizePhoneKey(identifier);
}

export function displayNameFor(record: ContactRecord): string | null {
  const first = record.firstName?.trim();
  const last = record.lastName?.trim();
  if (first && last) return `${first} ${last}`;
  if (first) return first;
  if (last) return last;
  const org = record.organization?.trim();
  return org || null;
}

export function resolveName(identifier: string, index: ContactIndex): string | null {
  const key = identifierKey(identifier);
  if (!key) return null;
  const record = index.get(key);
  return record ? displayNameFor(record) : null;
}

// --- IO: query one AddressBook-v22.abcddb file, read-only ---

const QUERY = `
  SELECT r.ZFIRSTNAME AS firstName, r.ZLASTNAME AS lastName, r.ZORGANIZATION AS org,
         p.ZFULLNUMBER AS phone, NULL AS email
  FROM ZABCDPHONENUMBER p
  JOIN ZABCDRECORD r ON r.Z_PK = p.ZOWNER
  UNION ALL
  SELECT r.ZFIRSTNAME, r.ZLASTNAME, r.ZORGANIZATION, NULL, e.ZADDRESS
  FROM ZABCDEMAILADDRESS e
  JOIN ZABCDRECORD r ON r.Z_PK = e.ZOWNER
`;

interface ContactRow {
  firstName: string | null;
  lastName: string | null;
  org: string | null;
  phone: string | null;
  email: string | null;
}

// Exported for the unit test, which builds a tiny fixture db with the same
// schema rather than touching a real AddressBook file.
export function indexOneDb(path: string, index: ContactIndex): void {
  const db = new Database(path, { readonly: true });
  try {
    const rows = db.query(QUERY).all() as ContactRow[];
    for (const row of rows) {
      const key = row.phone ? normalizePhoneKey(row.phone) : row.email ? normalizeEmailKey(row.email) : null;
      if (!key || index.has(key)) continue;
      index.set(key, { firstName: row.firstName, lastName: row.lastName, organization: row.org });
    }
  } finally {
    db.close();
  }
}

function findAddressBookDbs(): string[] {
  const sourcesDir = join(homedir(), 'Library', 'Application Support', 'AddressBook', 'Sources');
  let entries: string[];
  try {
    entries = readdirSync(sourcesDir);
  } catch {
    return [];
  }
  return entries
    .map((entry) => join(sourcesDir, entry, 'AddressBook-v22.abcddb'))
    .filter((dbPath) => existsSync(dbPath));
}

let cachedIndex: ContactIndex | null = null;

// Builds the map once, caches it in memory for the process lifetime. Never
// throws: any DB it can't open is skipped, and a machine with no readable
// AddressBook data (no Contacts, no Full Disk Access) just logs one warning
// and returns an empty index, so every lookup falls through to the raw
// identifier instead of crashing the server.
export function loadContactIndex(): ContactIndex {
  if (cachedIndex) return cachedIndex;
  const index: ContactIndex = new Map();
  const dbPaths = findAddressBookDbs();
  let opened = 0;
  for (const dbPath of dbPaths) {
    try {
      indexOneDb(dbPath, index);
      opened++;
    } catch (err) {
      console.warn(`contacts: failed to read ${dbPath}: ${err}`);
    }
  }
  if (opened === 0) {
    console.warn(
      'contacts: no AddressBook source was readable — falling back to raw identifiers ' +
        '(this machine may lack Contacts data or Full Disk Access)',
    );
  }
  cachedIndex = index;
  return index;
}
