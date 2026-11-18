// The site's 404 page, for on-demand pages that have to answer "not found".
//
// In `astro dev`, an empty 404 Response makes Astro render the 404 page. On Netlify it doesn't:
// the 404 page is prerendered, so it isn't in the server function, and the browser gets an
// empty 404 and shows its own error page. So the page is fetched from the site itself, where
// Netlify serves it as a file, and sent back with status 404 and the caller's cache headers,
// which decide, for example, that a closed door's 404 is never cached for a previewer.

/** How long to wait for the 404 page before answering without it. */
const TIMEOUT_MS = 5000;

export async function notFound(
  pageUrl: URL,
  headers: Record<string, string>,
  fetchFn: typeof fetch = fetch,
): Promise<Response> {
  try {
    const response = await fetchFn(new URL('/404.html', pageUrl), { signal: AbortSignal.timeout(TIMEOUT_MS) });
    const html = await response.text();
    // astro dev answers /404.html with a 404 too, but with the page, so the body is what counts.
    if (html.includes('<html')) {
      return new Response(html, { status: 404, headers: { ...headers, 'Content-Type': 'text/html; charset=utf-8' } });
    }
  } catch {
    // Unreachable or too slow: still a 404, just a plain one.
  }
  return new Response('not found', { status: 404, headers: { ...headers, 'Content-Type': 'text/plain; charset=utf-8' } });
}
