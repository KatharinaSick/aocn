// A stand-in for the API, for the accessibility tests of on-demand pages. It serves one
// made-up open door: the real ones live in the private challenges repository, never here.
// The text is long enough to wrap, and has every part a door page can show. Door 3 is open
// but can't be served, for the page that says to come back later. One made-up player has
// fixed door 1, for the profile card; another sees their own private card. Door 5 isn't open yet, and
// door 7 is a previewer's early look, before its interview is uploaded. Door 9 was never uploaded.
// The player is logged in, on one leaderboard they own, and has an invite to it.
import { createServer } from 'node:http';

const port = Number(process.argv[2] ?? 4801);

const door = {
  door: 1,
  opensAt: '2026-12-01T06:00:00+01:00',
  title: 'Example',
  story:
    'This is a made-up door for the accessibility tests. Its story is long enough to wrap over\nseveral lines at every width the tests check.\n\nA second paragraph, so the spacing between paragraphs is tested too.',
  mission: ['Fix the first thing that is broken', 'Keep everything else working', 'Give nothing more access than it needs'],
  pola: 'This door does not exist. The tests do not seem to mind.',
  videoId: 'example',
  maintainer: { name: 'Example Person', role: 'Example maintainer', url: 'https://example.com/person' },
  docs: [
    { title: 'Example docs', url: 'https://example.com/docs' },
    { title: 'More example docs', url: 'https://example.com/more' },
  ],
};

const BOARD_ID = 'Example000000000';
const JOIN_CODE = 'ExampleCode00000';

const profile = {
  login: 'example',
  avatarUrl: '',
  since: '2026-11-20T09:00:00Z',
  doors: [{ door: 1, solvedAt: '2026-12-01T06:30:00+01:00' }],
  public: true,
  own: false,
  leaderboards: [{ id: BOARD_ID, name: 'Example Team', rank: 1, members: 3, final: false }],
};

// A leaderboard as its owner sees it, so every part of the page is there: standings with a
// tie, the invite link and the settings. Its deadline is set, so the note about it shows.
const doorNumbers = [1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23];
const board = {
  id: BOARD_ID,
  name: 'Example Team, with a name long enough to wrap',
  public: true,
  openJoin: false,
  countsUntil: '2026-12-31T23:59:59.999+01:00',
  final: false,
  owner: { login: 'example', avatarUrl: '' },
  members: 3,
  maxMembers: 200,
  you: { loggedIn: true, member: true, owner: true },
  joinCode: JOIN_CODE,
  doors: doorNumbers.map((door) => ({ door, opensAt: `2026-12-${String(door).padStart(2, '0')}T06:00:00+01:00`, counts: door <= 3 })),
  standings: [
    { rank: 1, id: 1, login: 'example', avatarUrl: '', profilePublic: true, you: true, owner: true, points: 195, failedVerifies: 1, fixed: 2, doors: [{ door: 1, points: 95, hints: 1, failedVerifies: 1 }, { door: 3, points: 100, hints: 0, failedVerifies: 0 }] },
    { rank: 2, id: 2, login: 'another-example', avatarUrl: '', profilePublic: false, you: false, owner: false, points: 100, failedVerifies: 2, fixed: 1, doors: [{ door: 3, points: 100, hints: 0, failedVerifies: 2 }] },
    { rank: 2, id: 3, login: 'third-example', avatarUrl: '', profilePublic: false, you: false, owner: false, points: 100, failedVerifies: 2, fixed: 1, doors: [{ door: 1, points: 100, hints: 0, failedVerifies: 2 }] },
  ],
  together: 4,
};

createServer((req, res) => {
  res.setHeader('Content-Type', 'application/json');
  if (req.url === '/doors/1') {
    res.end(JSON.stringify(door));
  } else if (req.url === '/doors/3') {
    res.statusCode = 503;
    res.end('{"error":"this door can\'t be served right now, try again in a few minutes"}');
  } else if (req.url === '/doors/7') {
    res.end(JSON.stringify({ ...door, door: 7, opensAt: '2026-12-07T06:00:00+01:00', preview: true, videoId: '' }));
  } else if (req.url === '/doors/9') {
    res.statusCode = 503;
    res.end('{"error":"door 9 isn\'t ready yet","door":9,"notReady":true}');
  } else if (req.url === '/doors/5') {
    res.statusCode = 404;
    res.end('{"door":5,"opensAt":"2026-12-05T06:00:00+01:00"}');
  } else if (req.url === '/users/example') {
    res.end(JSON.stringify(profile));
  } else if (req.url === '/users/mine') {
    res.end(JSON.stringify({ ...profile, login: 'mine', public: false, own: true }));
  } else if (req.url === '/me') {
    res.end('{"login":"example","avatarUrl":""}');
  } else if (req.url === '/me/leaderboards') {
    res.end(JSON.stringify({ leaderboards: [{ id: BOARD_ID, name: board.name, public: true, own: true, members: 3, countsUntil: board.countsUntil, final: false, rank: 1 }], owned: 1, maxOwned: 3 }));
  } else if (req.url === `/leaderboards/${BOARD_ID}`) {
    res.end(JSON.stringify(board));
  } else if (req.url === `/join/${JOIN_CODE}`) {
    res.end(JSON.stringify({ name: board.name, public: false, countsUntil: board.countsUntil, final: false, owner: board.owner, members: 3, maxMembers: 200, full: false, member: false }));
  } else if (req.url === '/health') {
    res.end('{"status":"ok"}');
  } else {
    res.statusCode = 404;
    res.end('{"error":"no such door"}');
  }
}).listen(port);
