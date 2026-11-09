// A stand-in for the API, for the accessibility tests of on-demand pages. It serves one
// made-up open door: the real ones live in the private challenges repository, never here.
// The text is long enough to wrap, and has every part a door page can show. Door 2 is open
// but can't be served, for the page that says to come back later.
import { createServer } from 'node:http';

const port = Number(process.argv[2] ?? 4801);

const door = {
  door: 1,
  opensAt: '2026-12-01T06:00:00+01:00',
  title: 'Example',
  story:
    'This is a made-up door for the accessibility tests. Its story is long enough to wrap over\nseveral lines at every width the tests check.\n\nA second paragraph, so the spacing between paragraphs is tested too.',
  log: 'This door does not exist. The tests do not seem to mind.',
  videoId: 'example',
};

createServer((req, res) => {
  res.setHeader('Content-Type', 'application/json');
  if (req.url === '/doors/1') {
    res.end(JSON.stringify(door));
  } else if (req.url === '/doors/2') {
    res.statusCode = 503;
    res.end('{"error":"this door can\'t be served right now, try again in a few minutes"}');
  } else if (req.url === '/health') {
    res.end('{"status":"ok"}');
  } else {
    res.statusCode = 404;
    res.end('{"error":"no such door"}');
  }
}).listen(port);
