/**
 * The landing page's structured data (schema.org JSON-LD): the site, so search results can
 * show its name, and the season as an online event. Its dates come from the schedule.
 */
import { FIRST_DOOR, LAST_DOOR, doorOpensAt } from './doors';

const SITE_URL = 'https://aocn.dev/';
const SITE_NAME = 'Advent of Cloud Native';

export function landingStructuredData(description: string, imageUrl: string) {
  return {
    '@context': 'https://schema.org',
    '@graph': [
      {
        '@type': 'WebSite',
        name: SITE_NAME,
        alternateName: 'aocn.dev',
        url: SITE_URL,
      },
      {
        '@type': 'Event',
        name: SITE_NAME,
        description,
        image: imageUrl,
        url: SITE_URL,
        startDate: doorOpensAt(FIRST_DOOR).toISOString(),
        endDate: doorOpensAt(LAST_DOOR).toISOString(),
        eventStatus: 'https://schema.org/EventScheduled',
        eventAttendanceMode: 'https://schema.org/OnlineEventAttendanceMode',
        location: { '@type': 'VirtualLocation', url: SITE_URL },
        isAccessibleForFree: true,
        organizer: { '@type': 'Person', name: 'Katharina Sick', url: SITE_URL },
      },
    ],
  };
}

/**
 * The JSON for a `<script type="application/ld+json">`. `<` is escaped so no text in it
 * can close the script element early.
 */
export function toJsonLd(data: unknown): string {
  return JSON.stringify(data).replace(/</g, '\\u003c');
}
