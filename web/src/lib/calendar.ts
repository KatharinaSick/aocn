/**
 * The door schedule as an iCalendar file (RFC 5545), so people can import every door
 * opening into their own calendar. The site links to it for import; anyone who adds the
 * URL as a subscription by hand gets the same events.
 *
 * Every time comes from doors.ts. The events say nothing about what is behind a door.
 * UIDs are stable, so a subscribed calendar, or a second import, updates an event instead
 * of duplicating it.
 */
import { DOOR_NUMBERS, doorOpensAt } from './doors';

const CALENDAR_NAME = 'Advent of Cloud Native';
const EVENT_DURATION = 30 * 60 * 1000;

/** 20261201T050000Z */
function formatUtc(date: Date): string {
  return date.toISOString().replace(/[-:]/g, '').replace(/\.\d{3}/, '');
}

/** Escapes a TEXT value: backslash, semicolon, comma and line breaks. */
function escapeText(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/;/g, '\\;').replace(/,/g, '\\,').replace(/\r?\n/g, '\\n');
}

/**
 * Folds a content line so no line is longer than 75 octets; continuation lines start with
 * a space. Counted in UTF-8 bytes, and never splitting a character.
 */
export function foldLine(line: string): string {
  const encoder = new TextEncoder();
  const parts: string[] = [];
  let current = '';
  let currentBytes = 0;
  for (const char of line) {
    const bytes = encoder.encode(char).length;
    const limit = parts.length === 0 ? 75 : 74; // continuation lines lose one octet to the leading space
    if (currentBytes + bytes > limit) {
      parts.push(current);
      current = '';
      currentBytes = 0;
    }
    current += char;
    currentBytes += bytes;
  }
  parts.push(current);
  return parts.join('\r\n ');
}

export function doorEventUid(door: number): string {
  return `door-${String(door).padStart(2, '0')}@aocn.dev`;
}

/**
 * The whole calendar. `generatedAt` becomes each event's DTSTAMP; `siteUrl` is the link in
 * every event.
 */
export function buildCalendar(siteUrl: URL, generatedAt: Date): string {
  const link = siteUrl.href;
  const lines = [
    'BEGIN:VCALENDAR',
    'VERSION:2.0',
    'PRODID:-//aocn.dev//Advent of Cloud Native//EN',
    'CALSCALE:GREGORIAN',
    'METHOD:PUBLISH',
    `X-WR-CALNAME:${escapeText(CALENDAR_NAME)}`,
    `X-WR-CALDESC:${escapeText('Every door of Advent of Cloud Native, as it opens.')}`,
    // Ask subscribed calendars to check for changes once a day.
    'REFRESH-INTERVAL;VALUE=DURATION:P1D',
    'X-PUBLISHED-TTL:P1D',
  ];

  for (const door of DOOR_NUMBERS) {
    const opensAt = doorOpensAt(door);
    const number = String(door).padStart(2, '0');
    lines.push(
      'BEGIN:VEVENT',
      `UID:${doorEventUid(door)}`,
      `DTSTAMP:${formatUtc(generatedAt)}`,
      `DTSTART:${formatUtc(opensAt)}`,
      `DTEND:${formatUtc(new Date(opensAt.getTime() + EVENT_DURATION))}`,
      `SUMMARY:${escapeText(`aocn · door ${number} opens`)}`,
      `DESCRIPTION:${escapeText(`A new maintainer interview and challenge are up. ${link}`)}`,
      `URL:${link}`,
      // Shown as free time: a door opening should not block anyone's calendar.
      'TRANSP:TRANSPARENT',
      'END:VEVENT',
    );
  }

  lines.push('END:VCALENDAR');
  return lines.map(foldLine).join('\r\n') + '\r\n';
}
