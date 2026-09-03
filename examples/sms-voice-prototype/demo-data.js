// demo-data.js — entirely fake seed data for `--demo` mode. No content here
// is derived from any real chat.db; it mirrors the illustrative mockup in
// discussion #240 verbatim (already public in that thread).

export const DEMO_CHATS = [
  { id: 1, displayName: 'Joe', lastMessage: 'foo foo', lastMessageAt: '2026-09-03T18:00:00.000Z' },
  { id: 2, displayName: 'Ana', lastMessage: 'lunch tomorrow?', lastMessageAt: '2026-09-03T17:00:00.000Z' },
  { id: 3, displayName: 'Joe', lastMessage: 'see you sat', lastMessageAt: '2026-09-03T16:00:00.000Z' },
  { id: 4, displayName: 'Mom', lastMessage: 'call me when you can', lastMessageAt: '2026-09-03T15:00:00.000Z' },
];

export const DEMO_HISTORY = {
  1: [{ sender: 'Joe', text: 'foo foo', isFromMe: false, createdAt: '2026-09-03T18:00:00.000Z' }],
  2: [{ sender: 'Ana', text: 'lunch tomorrow?', isFromMe: false, createdAt: '2026-09-03T17:00:00.000Z' }],
  3: [{ sender: 'Joe', text: 'see you sat', isFromMe: false, createdAt: '2026-09-03T16:00:00.000Z' }],
  4: [{ sender: 'Mom', text: 'call me when you can', isFromMe: false, createdAt: '2026-09-03T15:00:00.000Z' }],
};
