// grammar.js — pure parser for discussion #240's step-scoped voice grammar.
//
//   utterance        = [ chat-selector ] [ draft-text ] [ trailing-command ] ;
//   chat-selector    = a name or stand-in word from the visible list ; (binds only in "list" screen)
//   draft-text       = free text, appended to the draft ;
//   trailing-command = "submit" | "scratch that" | "go back" | "read it back" ;
//
// Plain ES module, no build step: loaded directly by index.html AND imported
// by grammar.spec.ts under Bun. Keep this file dependency-free.

export const STAND_IN_WORDS = [
  'penguin', 'otter', 'maple', 'cobalt', 'harbor',
  'lighthouse', 'falcon', 'driftwood', 'ember', 'quartz',
];

// Longest phrase first so "read it back" is not shadowed by "go back".
const TRAILING_COMMANDS = [
  { name: 'read it back', tokens: ['read', 'it', 'back'] },
  { name: 'scratch that', tokens: ['scratch', 'that'] },
  { name: 'go back', tokens: ['go', 'back'] },
  { name: 'submit', tokens: ['submit'] },
];

export function tokenize(text) {
  return text.trim().split(/\s+/).filter(Boolean);
}

export function stripTrailingCommand(text) {
  const tokens = tokenize(text);
  for (const cmd of TRAILING_COMMANDS) {
    const n = cmd.tokens.length;
    if (tokens.length < n) continue;
    const tail = tokens.slice(tokens.length - n).map((t) => t.toLowerCase());
    if (tail.join(' ') === cmd.tokens.join(' ')) {
      return { command: cmd.name, remainder: tokens.slice(0, tokens.length - n).join(' ') };
    }
  }
  return { command: null, remainder: tokens.join(' ') };
}

// contacts: [{ id, displayName, standIn, selectors: [tokens, ...] }]
export function matchSelector(text, contacts) {
  const tokens = tokenize(text);
  const candidates = [];
  for (const contact of contacts) {
    for (const selTokens of contact.selectors) {
      candidates.push({ contact, selTokens });
    }
  }
  candidates.sort((a, b) => b.selTokens.length - a.selTokens.length);
  for (const { contact, selTokens } of candidates) {
    const n = selTokens.length;
    if (tokens.length < n) continue;
    const head = tokens.slice(0, n).map((t) => t.toLowerCase());
    if (head.join(' ') === selTokens.join(' ')) {
      return { contact, remainder: tokens.slice(n).join(' ') };
    }
  }
  return null;
}

// Parses one utterance against the rules bound in `screen`. Selector binding
// is only ever attempted when screen === 'list' — this state-scoping is the
// whole fix for the name-vs-prose collision (discussion #240 ruling 1).
export function parseUtterance(rawText, screen, contacts) {
  const { command, remainder } = stripTrailingCommand(rawText);
  let selector = null;
  let draftText = remainder;
  if (screen === 'list') {
    const m = matchSelector(remainder, contacts);
    if (m) {
      selector = m.contact;
      draftText = m.remainder;
    }
  }
  return { selector, draftText: draftText.trim(), trailingCommand: command };
}

export function appendText(draft, text) {
  if (!text) return draft;
  return draft ? `${draft} ${text}` : text;
}

export function initialState() {
  return { screen: 'list', chatId: null, draft: '', parkedDrafts: {} };
}

// Applies one utterance to app state. Returns { state, actions, parsed }.
// actions is a list of side-effect requests for the caller (UI/server) to
// carry out: { type: 'submit', chatId, text } | { type: 'readItBack', text }.
export function applyUtterance(state, rawText, contacts) {
  const parsed = parseUtterance(rawText, state.screen, contacts);
  const next = { ...state, parkedDrafts: { ...state.parkedDrafts } };
  const actions = [];

  if (state.screen === 'list') {
    if (parsed.selector) {
      const chatId = parsed.selector.id;
      next.screen = 'compose';
      next.chatId = chatId;
      next.draft = appendText(next.parkedDrafts[chatId] || '', parsed.draftText);
      delete next.parkedDrafts[chatId];
    }
    return { state: next, actions, parsed };
  }

  if (state.screen === 'compose') {
    next.draft = appendText(next.draft, parsed.draftText);
    applyTrailing(next, actions, parsed.trailingCommand);
    return { state: next, actions, parsed };
  }

  // screen === 'sent'
  if (parsed.trailingCommand === 'go back') {
    next.screen = 'list';
    next.chatId = null;
    next.draft = '';
    return { state: next, actions, parsed };
  }
  if (parsed.draftText || parsed.trailingCommand) {
    // Sent --> Composing: more text starts a fresh draft on the same chat.
    next.screen = 'compose';
    next.draft = appendText('', parsed.draftText);
    applyTrailing(next, actions, parsed.trailingCommand);
  }
  return { state: next, actions, parsed };
}

function applyTrailing(next, actions, trailingCommand) {
  if (trailingCommand === 'scratch that') {
    next.draft = '';
  } else if (trailingCommand === 'go back') {
    next.parkedDrafts[next.chatId] = next.draft;
    next.screen = 'list';
    next.draft = '';
    next.chatId = null;
  } else if (trailingCommand === 'submit') {
    actions.push({ type: 'submit', chatId: next.chatId, text: next.draft });
    next.screen = 'sent';
  } else if (trailingCommand === 'read it back') {
    actions.push({ type: 'readItBack', text: next.draft });
  }
}

// chats: [{ id, displayName }] in the order they should be listed (recency).
// First names win for speakability: a chat with a unique first name across
// the list registers BOTH the bare first name and the full name as selector
// vocabulary ("ana" and "ana lopez" both open the same chat). A duplicate
// first name falls back to first+last for every contact sharing it; if even
// the full name repeats, later occurrences of that full name fall back
// further to a rare stand-in word, per discussion #240 ruling 3 ("parlay
// nickname" mechanism).
export function assignStandIns(chats) {
  const firstNameCounts = new Map();
  const fullNameCounts = new Map();
  for (const chat of chats) {
    const tokens = tokenize(chat.displayName);
    const first = (tokens[0] || '').toLowerCase();
    const full = chat.displayName.trim().toLowerCase();
    firstNameCounts.set(first, (firstNameCounts.get(first) || 0) + 1);
    fullNameCounts.set(full, (fullNameCounts.get(full) || 0) + 1);
  }

  const seenFullName = new Map();
  let wordIdx = 0;
  return chats.map((chat) => {
    const tokens = tokenize(chat.displayName);
    const firstTokens = tokens.slice(0, 1).map((t) => t.toLowerCase());
    const fullTokens = tokens.map((t) => t.toLowerCase());
    const first = firstTokens[0] || '';
    const full = chat.displayName.trim().toLowerCase();

    const firstNameUnique = firstNameCounts.get(first) === 1;
    const fullNameUnique = fullNameCounts.get(full) === 1;

    let standIn = null;
    const selectors = [];

    if (firstNameUnique) {
      selectors.push(firstTokens);
      if (fullTokens.length > 1) selectors.push(fullTokens);
    } else if (fullNameUnique) {
      // Duplicate first name, but the full name still distinguishes it.
      selectors.push(fullTokens);
    } else {
      // Even the full name repeats: first occurrence keeps the full name,
      // every later occurrence gets a stand-in word instead.
      const occurrence = seenFullName.get(full) || 0;
      seenFullName.set(full, occurrence + 1);
      if (occurrence === 0) {
        selectors.push(fullTokens);
      } else {
        standIn = STAND_IN_WORDS[wordIdx % STAND_IN_WORDS.length];
        wordIdx++;
        selectors.push([standIn]);
      }
    }

    return { id: chat.id, displayName: chat.displayName, standIn, selectors };
  });
}

export function sayNextBar(screen) {
  if (screen === 'list') {
    return [
      'Say a name to open that chat.',
      'Words after the name start your message.',
      '(duplicate names: say the word in parens)',
    ];
  }
  if (screen === 'compose') {
    return [
      'Keep talking to add to the message.',
      '"submit" to send · "scratch that" to clear',
      '"read it back" to hear it · "go back" for the list',
    ];
  }
  return ['Keep talking to start another message.', '"go back" for your messages list.'];
}
