// The interview's thumbnail for a door page, served from aocn.dev. The function fetches it from
// YouTube, so a visitor's browser sends nothing to YouTube until they start the video.
//
// Behind the same gate as the page: the API decides whether the door is open (or a preview for
// this visitor), and only then is its video ID known. Cached like the page.
import type { APIRoute } from 'astro';
import { SESSION_COOKIE, doorCacheHeaders, fetchDoor, fetchThumbnail } from '../../../lib/doorPage';
import { isDoor } from '../../../lib/doors';

export const prerender = false;

export const GET: APIRoute = async ({ params, cookies }) => {
  const answer = await fetchDoor(import.meta.env.API_URL, params.door ?? '', isDoor, {
    session: cookies.get(SESSION_COOKIE)?.value,
  });
  if (answer.state !== 'open' || !answer.page.videoId) {
    return new Response(null, { status: 404, headers: { 'Cache-Control': 'no-store' } });
  }
  const image = await fetchThumbnail(answer.page.videoId);
  if (!image) {
    return new Response(null, { status: 502, headers: { 'Cache-Control': 'no-store' } });
  }
  return new Response(image, { headers: { 'Content-Type': 'image/jpeg', ...doorCacheHeaders(answer) } });
};
