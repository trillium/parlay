// contacts.spec.ts — unit tests for the AddressBook resolver (contacts.ts).
// Run with: cd examples/sms-voice-prototype && bun test
// Deliberately named .spec.ts, not .test.ts — see grammar.spec.ts's header
// comment for why (this prototype lives outside CI's repo-wide
// `git ls-files '*.test.ts'` coverage gate).
//
// The DB-touching test builds a tiny fixture sqlite file with the same
// schema as a real AddressBook-v22.abcddb rather than reading anything real.

import { Database } from 'bun:sqlite';
import { afterEach, describe, expect, test } from 'bun:test';
import { unlinkSync } from 'node:fs';
import {
  displayNameFor,
  identifierKey,
  indexOneDb,
  normalizeEmailKey,
  normalizePhoneKey,
  resolveName,
  type ContactIndex,
} from './contacts.ts';

describe('normalizePhoneKey', () => {
  test('strips punctuation and takes the last 10 digits', () => {
    expect(normalizePhoneKey('+1 (555) 867-5309')).toBe('5558675309');
    expect(normalizePhoneKey('555.867.5309')).toBe('5558675309');
    expect(normalizePhoneKey('15558675309')).toBe('5558675309');
  });

  test('rejects short numbers', () => {
    expect(normalizePhoneKey('12345')).toBeNull();
  });
});

describe('normalizeEmailKey', () => {
  test('lowercases and trims', () => {
    expect(normalizeEmailKey('  Someone@Example.com  ')).toBe('someone@example.com');
  });

  test('empty string is null', () => {
    expect(normalizeEmailKey('   ')).toBeNull();
  });
});

describe('identifierKey', () => {
  test('routes emails and phones to the right normalizer', () => {
    expect(identifierKey('someone@example.com')).toBe('someone@example.com');
    expect(identifierKey('+1 (555) 867-5309')).toBe('5558675309');
  });
});

describe('displayNameFor', () => {
  test('prefers first + last', () => {
    expect(displayNameFor({ firstName: 'Ana', lastName: 'Lopez', organization: null })).toBe('Ana Lopez');
  });

  test('falls back to first name alone', () => {
    expect(displayNameFor({ firstName: 'Ana', lastName: null, organization: null })).toBe('Ana');
  });

  test('falls back to last name alone', () => {
    expect(displayNameFor({ firstName: null, lastName: 'Lopez', organization: null })).toBe('Lopez');
  });

  test('falls back to organization when no name is set', () => {
    expect(displayNameFor({ firstName: null, lastName: null, organization: 'Acme Dental' })).toBe('Acme Dental');
  });

  test('null when nothing is set', () => {
    expect(displayNameFor({ firstName: null, lastName: null, organization: null })).toBeNull();
  });
});

describe('resolveName', () => {
  test('resolves a known phone key, ignores formatting differences', () => {
    const index: ContactIndex = new Map([['5558675309', { firstName: 'Ana', lastName: 'Lopez', organization: null }]]);
    expect(resolveName('+1 (555) 867-5309', index)).toBe('Ana Lopez');
  });

  test('unknown identifier resolves to null', () => {
    const index: ContactIndex = new Map();
    expect(resolveName('+15558675309', index)).toBeNull();
  });
});

describe('indexOneDb (fixture AddressBook-v22.abcddb schema)', () => {
  const fixturePath = `${import.meta.dir}/.contacts-fixture-test.sqlite`;

  afterEach(() => {
    try {
      unlinkSync(fixturePath);
    } catch {
      // already removed or never created
    }
  });

  function buildFixture(): void {
    const db = new Database(fixturePath, { create: true });
    db.exec(`
      CREATE TABLE ZABCDRECORD (Z_PK INTEGER PRIMARY KEY, ZFIRSTNAME TEXT, ZLASTNAME TEXT, ZORGANIZATION TEXT);
      CREATE TABLE ZABCDPHONENUMBER (Z_PK INTEGER PRIMARY KEY, ZFULLNUMBER TEXT, ZOWNER INTEGER);
      CREATE TABLE ZABCDEMAILADDRESS (Z_PK INTEGER PRIMARY KEY, ZADDRESS TEXT, ZOWNER INTEGER);
    `);
    db.run(`INSERT INTO ZABCDRECORD (Z_PK, ZFIRSTNAME, ZLASTNAME, ZORGANIZATION) VALUES (1, 'Ana', 'Lopez', NULL)`);
    db.run(`INSERT INTO ZABCDRECORD (Z_PK, ZFIRSTNAME, ZLASTNAME, ZORGANIZATION) VALUES (2, NULL, NULL, 'Acme Dental')`);
    db.run(`INSERT INTO ZABCDPHONENUMBER (ZFULLNUMBER, ZOWNER) VALUES ('+1 (555) 867-5309', 1)`);
    db.run(`INSERT INTO ZABCDEMAILADDRESS (ZADDRESS, ZOWNER) VALUES ('Ana@Example.com', 1)`);
    db.run(`INSERT INTO ZABCDPHONENUMBER (ZFULLNUMBER, ZOWNER) VALUES ('555-222-3333', 2)`);
    db.close();
  }

  test('indexes both phone and email rows, joined back to the owning record', () => {
    buildFixture();
    const index: ContactIndex = new Map();
    indexOneDb(fixturePath, index);

    expect(resolveName('15558675309', index)).toBe('Ana Lopez');
    expect(resolveName('ana@example.com', index)).toBe('Ana Lopez');
    expect(resolveName('555-222-3333', index)).toBe('Acme Dental');
    expect(resolveName('+19995550000', index)).toBeNull();
  });

  test('a later db does not overwrite an already-indexed key', () => {
    buildFixture();
    const index: ContactIndex = new Map([['5558675309', { firstName: 'First', lastName: 'Wins', organization: null }]]);
    indexOneDb(fixturePath, index);
    expect(resolveName('5558675309', index)).toBe('First Wins');
  });
});
