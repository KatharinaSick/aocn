/**
 * Reads schedule.yaml at the repository root, the one place a date lives. The API reads
 * the same file (api/internal/schedule) and must accept and reject exactly what this
 * does; the shared files in testdata/ check that.
 *
 * Runs at build time only, in the Vite plugin (web/plugins/schedule.ts): the browser
 * gets the parsed timestamps, never the YAML or this parser.
 *
 * Dates stay strings until they are checked here. YAML 1.2, which `yaml` follows, has no
 * timestamp type, and yaml.v3 decodes them into Go strings, so neither side guesses: a
 * date needs a date, a time and an offset, or it is rejected.
 */
import { isMap, isScalar, parseAllDocuments, type Scalar } from 'yaml';

export interface Schedule {
  /**
   * The doors by number, each with when it opens in milliseconds since the epoch. A door's
   * number is its day in December (1, 3, 5, …), written out in the file rather than worked
   * out from its date, so a rehearsal can move a date without renumbering the door.
   */
  doors: { door: number; opensAt: number }[];
  /** The two moments POLARIS-7's incident counter counts from, in milliseconds. */
  christmasEve: { beforeSeason: number; finale: number };
}

// The same pattern as in the Go package: seconds, no fractions, and Z or a ±HH:MM offset.
const DATE = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(Z|([+-])(\d{2}):(\d{2}))$/;

const MINUTE = 60 * 1000;

/** How a door's number is written: 1, 3, … 23, never 01, +1 or 1.0. The same as in Go. */
const DOOR_NUMBER = /^[1-9][0-9]*$/;

/** Parses schedule.yaml. Throws an Error naming the door or key that is wrong. */
export function parseSchedule(text: string): Schedule {
  // parseAllDocuments rather than parse, so a second document after --- is refused here
  // the way the Go side refuses it, instead of being reported as a syntax error. It does
  // not throw: syntax errors sit on each document, and are reported before anything else.
  // uniqueKeys is off so a door listed twice gets the same error as on the Go side,
  // from the check below, instead of the library's own.
  const documents = parseAllDocuments(text, { uniqueKeys: false });
  for (const document of documents) {
    if (document.errors.length > 0) throw new Error(`not valid YAML: ${document.errors[0].message}`);
  }
  // A trailing --- counts too: it starts a second, empty document in both libraries.
  if (documents.length > 1) throw new Error('must be a single YAML document');
  const file: unknown = documents.length === 1 ? documents[0].toJS() : null;

  const top = asMapping(file, 'schedule', ['doors', 'christmasEve']);

  // The doors as nodes, in the order of the file: toJS would sort the numbers itself.
  const doorsNode = documents[0]?.get('doors', true);
  if (!isMap(doorsNode) || doorsNode.items.length === 0) {
    throw new Error('doors: must map door numbers to dates, with at least one door');
  }
  const doors: Schedule['doors'] = [];
  for (const { key, value } of doorsNode.items) {
    const source = isScalar(key) ? String((key as Scalar).source ?? key.value) : String(key);
    if (!isScalar(key) || key.type !== 'PLAIN' || !DOOR_NUMBER.test(source)) {
      throw new Error(`doors: "${source}" is not a door number, like 1 or 23`);
    }
    const door = Number(source);
    const previous = doors.at(-1);
    if (previous && door === previous.door) throw new Error(`door ${door}: listed twice`);
    if (previous && door < previous.door) {
      throw new Error(`door ${door}: listed after door ${previous.door}; list the doors by number`);
    }
    const raw = isScalar(value) ? value.toJSON() : value;
    if (value !== null && !isScalar(value)) throw new Error(`door ${door}: not a date`);
    const opensAt = parseDate(raw, `door ${door}`);
    if (previous && opensAt <= previous.opensAt) {
      throw new Error(`door ${door}: opens at ${String(raw)}, not after door ${previous.door}`);
    }
    doors.push({ door, opensAt });
  }

  const christmasEve = asMapping(top.christmasEve ?? {}, 'christmasEve', ['beforeSeason', 'finale']);
  return {
    doors,
    christmasEve: {
      beforeSeason: parseDate(christmasEve.beforeSeason, 'christmasEve.beforeSeason'),
      finale: parseDate(christmasEve.finale, 'christmasEve.finale'),
    },
  };
}

/** A mapping with only the given keys, like yaml.v3 with KnownFields on the Go side. */
function asMapping(value: unknown, label: string, keys: string[]): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error(`${label}: must be a mapping with ${keys.join(' and ')}`);
  }
  for (const key of Object.keys(value)) {
    if (!keys.includes(key)) throw new Error(`${label}: unknown key "${key}"`);
  }
  return value as Record<string, unknown>;
}

function parseDate(value: unknown, label: string): number {
  if (value === null || value === undefined || value === '') {
    throw new Error(`${label}: missing date`);
  }
  // yaml.v3 hands a number or a boolean to Go as its text, so the message matches.
  const text = typeof value === 'number' || typeof value === 'boolean' ? String(value) : value;
  if (typeof text !== 'string') throw new Error(`${label}: not a date`);

  const match = DATE.exec(text);
  if (!match) {
    throw new Error(`${label}: "${text}" needs a date, a time and an offset, like 2026-12-01T06:00:00+01:00`);
  }

  const [, year, month, day, hour, minute, second, , , offsetHours, offsetMinutes] = match.map(Number);
  // Not Date.UTC, which reads years 0 to 99 as 1900 to 1999. Year, month and day are set in
  // one call, so a valid Feb 29 cannot roll over while the year is still the old one.
  const date = new Date(0);
  date.setUTCFullYear(year, month - 1, day);
  date.setUTCHours(hour, minute, second, 0);
  const local = date.getTime();
  // The Date rolls Feb 30 over into March instead of refusing it, so check it came back unchanged.
  const back = new Date(local);
  const valid =
    back.getUTCFullYear() === year &&
    back.getUTCMonth() === month - 1 &&
    back.getUTCDate() === day &&
    back.getUTCHours() === hour &&
    back.getUTCMinutes() === minute &&
    back.getUTCSeconds() === second &&
    (match[7] === 'Z' || (offsetHours <= 23 && offsetMinutes <= 59));
  if (!valid) throw new Error(`${label}: "${text}" is not a valid date`);

  if (match[7] === 'Z') return local;
  const offset = (offsetHours * 60 + offsetMinutes) * MINUTE;
  return match[8] === '+' ? local - offset : local + offset;
}
