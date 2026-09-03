// grammar.spec.ts — unit tests for the pure grammar parser (grammar.js).
// Run with: cd examples/sms-voice-prototype && bun test
// Deliberately named .spec.ts, not .test.ts: this prototype lives outside
// the bun workspace (root workspaces glob is packages/*) and outside CI's
// `bun test` roots list, so it must also stay outside CI's repo-wide
// `git ls-files '*.test.ts'` coverage gate. See README.md.

import { describe, expect, test } from 'bun:test';
import {
  applyUtterance,
  assignStandIns,
  initialState,
  matchSelector,
  parseUtterance,
  stripTrailingCommand,
} from './grammar.js';

function contactsFor(chats) {
  return assignStandIns(chats);
}

describe('stripTrailingCommand', () => {
  test('strips a single-word trailing command', () => {
    expect(stripTrailingCommand('Baz Baz submit')).toEqual({ command: 'submit', remainder: 'Baz Baz' });
  });

  test('strips a multi-word trailing command', () => {
    expect(stripTrailingCommand('bar bar go back')).toEqual({ command: 'go back', remainder: 'bar bar' });
    expect(stripTrailingCommand('scratch that')).toEqual({ command: 'scratch that', remainder: '' });
    expect(stripTrailingCommand('read it back')).toEqual({ command: 'read it back', remainder: '' });
  });

  test('prefers the longer phrase so "back" is not mis-stripped as "go back"', () => {
    expect(stripTrailingCommand('please read it back')).toEqual({ command: 'read it back', remainder: 'please' });
  });

  test('leaves text untouched with no trailing command', () => {
    expect(stripTrailingCommand('bar bar')).toEqual({ command: null, remainder: 'bar bar' });
  });
});

describe('assignStandIns + matchSelector', () => {
  test('first occurrence of a name selects by name alone', () => {
    const contacts = contactsFor([{ id: 1, displayName: 'Joe' }]);
    expect(contacts[0].standIn).toBeNull();
    const m = matchSelector('Joe bar bar', contacts);
    expect(m?.contact.id).toBe(1);
    expect(m?.remainder).toBe('bar bar');
  });

  test('duplicate names get a stand-in word that also binds as selector', () => {
    const contacts = contactsFor([
      { id: 1, displayName: 'Joe' },
      { id: 2, displayName: 'Ana' },
      { id: 3, displayName: 'Joe' },
    ]);
    expect(contacts[2].standIn).toBe('penguin');
    // The bare duplicate name no longer resolves to the second Joe...
    const byName = matchSelector('Joe bar bar', contacts);
    expect(byName?.contact.id).toBe(1);
    // ...but the stand-in does.
    const byStandIn = matchSelector('penguin bar bar', contacts);
    expect(byStandIn?.contact.id).toBe(3);
  });

  test('unmatched leading text returns null', () => {
    const contacts = contactsFor([{ id: 1, displayName: 'Joe' }]);
    expect(matchSelector('Nobody here', contacts)).toBeNull();
  });

  test('a unique first name registers both the bare first name and the full name', () => {
    const contacts = contactsFor([{ id: 1, displayName: 'Ana Lopez' }]);
    expect(contacts[0].standIn).toBeNull();
    expect(matchSelector('ana bar bar', contacts)?.contact.id).toBe(1);
    expect(matchSelector('ana lopez bar bar', contacts)?.contact.id).toBe(1);
  });

  test('duplicate first names fall back to first+last, dropping the bare first-name selector', () => {
    const contacts = contactsFor([
      { id: 1, displayName: 'Ana Lopez' },
      { id: 2, displayName: 'Ana Chen' },
    ]);
    expect(contacts[0].standIn).toBeNull();
    expect(contacts[1].standIn).toBeNull();
    expect(matchSelector('ana bar bar', contacts)).toBeNull();
    expect(matchSelector('ana lopez bar bar', contacts)?.contact.id).toBe(1);
    expect(matchSelector('ana chen bar bar', contacts)?.contact.id).toBe(2);
  });

  test('same first AND last name falls all the way back to a stand-in word', () => {
    const contacts = contactsFor([
      { id: 1, displayName: 'Ana Lopez' },
      { id: 2, displayName: 'Ana Lopez' },
    ]);
    expect(contacts[0].standIn).toBeNull();
    expect(contacts[1].standIn).toBe('penguin');
    expect(matchSelector('ana lopez bar bar', contacts)?.contact.id).toBe(1);
    expect(matchSelector('penguin bar bar', contacts)?.contact.id).toBe(2);
  });
});

describe('parseUtterance step-scoping', () => {
  const contacts = contactsFor([
    { id: 1, displayName: 'Joe' },
    { id: 2, displayName: 'Ana' },
  ]);

  test('list screen: leading name binds as selector', () => {
    const parsed = parseUtterance('Joe bar bar', 'list', contacts);
    expect(parsed.selector?.id).toBe(1);
    expect(parsed.draftText).toBe('bar bar');
    expect(parsed.trailingCommand).toBeNull();
  });

  test('compose screen: leading name is plain prose, never a selector', () => {
    const parsed = parseUtterance('Ana said she would come', 'compose', contacts);
    expect(parsed.selector).toBeNull();
    expect(parsed.draftText).toBe('Ana said she would come');
  });
});

describe('canonical trace (discussion #240)', () => {
  test('"Joe bar bar" then "Baz Baz submit" sends "bar bar Baz Baz"', () => {
    const contacts = contactsFor([{ id: 1, displayName: 'Joe' }]);
    let state = initialState();

    const step1 = applyUtterance(state, 'Joe bar bar', contacts);
    state = step1.state;
    expect(state.screen).toBe('compose');
    expect(state.chatId).toBe(1);
    expect(state.draft).toBe('bar bar');
    expect(step1.actions).toEqual([]);

    const step2 = applyUtterance(state, 'Baz Baz submit', contacts);
    state = step2.state;
    expect(state.draft).toBe('bar bar Baz Baz');
    expect(state.screen).toBe('sent');
    expect(step2.actions).toEqual([{ type: 'submit', chatId: 1, text: 'bar bar Baz Baz' }]);
  });
});

describe('trailing commands inside compose', () => {
  const contacts = contactsFor([{ id: 1, displayName: 'Joe' }]);

  test('"scratch that" clears the draft', () => {
    let state = applyUtterance(initialState(), 'Joe bar bar', contacts).state;
    state = applyUtterance(state, 'scratch that', contacts).state;
    expect(state.draft).toBe('');
    expect(state.screen).toBe('compose');
  });

  test('"go back" parks the draft and returns to the list', () => {
    let state = applyUtterance(initialState(), 'Joe bar bar', contacts).state;
    state = applyUtterance(state, 'go back', contacts).state;
    expect(state.screen).toBe('list');
    expect(state.chatId).toBeNull();
    expect(state.parkedDrafts[1]).toBe('bar bar');
  });

  test('re-selecting the same chat restores the parked draft', () => {
    let state = applyUtterance(initialState(), 'Joe bar bar', contacts).state;
    state = applyUtterance(state, 'go back', contacts).state;
    state = applyUtterance(state, 'Joe more', contacts).state;
    expect(state.draft).toBe('bar bar more');
  });

  test('"read it back" emits a readItBack action without changing the draft', () => {
    let state = applyUtterance(initialState(), 'Joe bar bar', contacts).state;
    const result = applyUtterance(state, 'read it back', contacts);
    expect(result.actions).toEqual([{ type: 'readItBack', text: 'bar bar' }]);
    expect(result.state.draft).toBe('bar bar');
  });

  test('no chat switching from within compose (ruling 2): "Ana" is prose, not a switch', () => {
    const twoContacts = contactsFor([
      { id: 1, displayName: 'Joe' },
      { id: 2, displayName: 'Ana' },
    ]);
    let state = applyUtterance(initialState(), 'Joe bar bar', twoContacts).state;
    state = applyUtterance(state, 'Ana would love this', twoContacts).state;
    expect(state.chatId).toBe(1);
    expect(state.draft).toBe('bar bar Ana would love this');
  });
});

describe('sent screen transitions', () => {
  test('more text after sent starts a fresh draft on the same chat', () => {
    const contacts = contactsFor([{ id: 1, displayName: 'Joe' }]);
    let state = applyUtterance(initialState(), 'Joe bar bar', contacts).state;
    state = applyUtterance(state, 'submit', contacts).state;
    expect(state.screen).toBe('sent');

    const result = applyUtterance(state, 'new message', contacts);
    expect(result.state.screen).toBe('compose');
    expect(result.state.chatId).toBe(1);
    expect(result.state.draft).toBe('new message');
  });

  test('"go back" from sent returns to the list', () => {
    const contacts = contactsFor([{ id: 1, displayName: 'Joe' }]);
    let state = applyUtterance(initialState(), 'Joe bar bar', contacts).state;
    state = applyUtterance(state, 'submit', contacts).state;
    state = applyUtterance(state, 'go back', contacts).state;
    expect(state.screen).toBe('list');
    expect(state.chatId).toBeNull();
  });
});
