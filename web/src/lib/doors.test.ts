import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import * as real from './doors';
import {
  doorsOf,
  formatStationClock,
  formatStationDay,
  formatStationDayLong,
  formatStationTime,
  formatStationTimeLong,
} from './doors';
import { parseSchedule } from './schedule';

// The rules are tested on a season written here, shaped like this year's: twelve doors
// every other day from Dec 1 2026 at 06:00 CET, and Christmas Eve on either side. Tests
// of schedule.yaml itself, which may change, are in schedule.test.ts.
const season = doorsOf({
  doors: Array.from({ length: 12 }, (_, i) => ({ door: 1 + 2 * i, opensAt: Date.UTC(2026, 11, 1 + 2 * i, 5) })),
  christmasEve: { beforeSeason: Date.UTC(2025, 11, 24, 5), finale: Date.UTC(2026, 11, 24, 5) },
});
const { numbers: DOOR_NUMBERS, doorOpensAt, incidentDays, isDoorOpen, nextDoor, seasonPhase } = season;

// Every time below is written in UTC. 06:00 CET is 05:00 UTC.
const at = (iso: string) => new Date(iso);
const MS = 1;

describe('the exports', () => {
  it('follow schedule.yaml', () => {
    const schedule = parseSchedule(readFileSync(new URL('../../../schedule.yaml', import.meta.url), 'utf8'));
    expect(real.DOOR_COUNT).toBe(schedule.doors.length);
    expect(real.DOOR_NUMBERS).toEqual(schedule.doors.map((d) => d.door));
    for (const { door, opensAt } of schedule.doors) {
      expect(real.doorOpensAt(door).getTime()).toBe(opensAt);
    }
    expect(real.FIRST_DOOR).toBe(schedule.doors[0].door);
    expect(real.LAST_DOOR).toBe(schedule.doors.at(-1)?.door);
    expect(real.seasonPhase(new Date(schedule.christmasEve.finale))).toBe('after');
    expect(real.incidentDays(new Date(schedule.christmasEve.beforeSeason))).toBe(0);
  });
});

describe('doorOpensAt', () => {
  it('opens door 1 on Dec 1 2026 at 06:00 CET', () => {
    expect(doorOpensAt(1).toISOString()).toBe('2026-12-01T05:00:00.000Z');
  });

  it('opens door 23 on Dec 23 2026 at 06:00 CET', () => {
    expect(doorOpensAt(23).toISOString()).toBe('2026-12-23T05:00:00.000Z');
  });

  it('opens a door every other day at the same time', () => {
    const expected = [1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23].map(
      (day) => `2026-12-${String(day).padStart(2, '0')}T05:00:00.000Z`,
    );
    const actual = DOOR_NUMBERS.map((door) => doorOpensAt(door).toISOString());
    expect(actual).toEqual(expected);
  });

  // 2 and 12 are days without a door; 24 is after the last.
  it.each([0, 2, 12, 24, -1, 1.5, Number.NaN])('rejects door %s', (door) => {
    expect(() => doorOpensAt(door)).toThrow(RangeError);
  });
});

describe('isDoorOpen', () => {
  it('keeps door 1 closed until the exact millisecond it opens', () => {
    expect(isDoorOpen(1, at('2026-12-01T04:59:59.999Z'))).toBe(false);
    expect(isDoorOpen(1, at('2026-12-01T05:00:00.000Z'))).toBe(true);
  });

  it('keeps door 23 closed until the exact millisecond it opens', () => {
    expect(isDoorOpen(23, at('2026-12-23T04:59:59.999Z'))).toBe(false);
    expect(isDoorOpen(23, at('2026-12-23T05:00:00.000Z'))).toBe(true);
  });

  it('keeps every door closed one millisecond before it opens, and open from then on', () => {
    for (const door of DOOR_NUMBERS) {
      const opensAt = doorOpensAt(door).getTime();
      expect(isDoorOpen(door, new Date(opensAt - MS))).toBe(false);
      expect(isDoorOpen(door, new Date(opensAt))).toBe(true);
      expect(isDoorOpen(door, at('2027-06-01T00:00:00.000Z'))).toBe(true);
    }
  });

  it('opens no door before the season', () => {
    const now = at('2026-10-01T12:00:00.000Z');
    for (const door of DOOR_NUMBERS) expect(isDoorOpen(door, now)).toBe(false);
  });

  it('does not open a door earlier for a player east of CET', () => {
    // 06:30 in Tokyo on Dec 1 is still the night before in CET.
    expect(isDoorOpen(1, at('2026-12-01T06:30:00+09:00'))).toBe(false);
  });

  it('rejects an invalid door instead of answering', () => {
    expect(() => isDoorOpen(2, at('2027-01-01T00:00:00.000Z'))).toThrow(RangeError);
  });
});

describe('nextDoor', () => {
  it('points to door 1 before the season', () => {
    expect(nextDoor(at('2026-10-01T12:00:00.000Z'))).toEqual({ door: 1, opensAt: doorOpensAt(1) });
  });

  it('moves on at the exact moment a door opens', () => {
    expect(nextDoor(at('2026-12-01T04:59:59.999Z'))?.door).toBe(1);
    expect(nextDoor(at('2026-12-01T05:00:00.000Z'))?.door).toBe(3);
  });

  it('is null once the last door is open', () => {
    expect(nextDoor(at('2026-12-23T04:59:59.999Z'))?.door).toBe(23);
    expect(nextDoor(at('2026-12-23T05:00:00.000Z'))).toBeNull();
  });
});

describe('incidentDays', () => {
  it('counts up from Christmas Eve 2025 before the season', () => {
    expect(incidentDays(at('2025-12-24T05:00:00.000Z'))).toBe(0);
    expect(incidentDays(at('2025-12-25T04:59:59.999Z'))).toBe(0);
    expect(incidentDays(at('2025-12-25T05:00:00.000Z'))).toBe(1);
    expect(incidentDays(at('2026-10-01T05:00:00.000Z'))).toBe(281);
    expect(incidentDays(at('2026-12-01T04:59:59.999Z'))).toBe(341);
  });

  it('flips at 06:00 CET, not at midnight', () => {
    expect(incidentDays(at('2026-10-01T04:59:59.999Z'))).toBe(280);
    expect(incidentDays(at('2026-10-01T05:00:00.000Z'))).toBe(281);
  });

  it('is 00 on a door day and 01 on the day between doors', () => {
    expect(incidentDays(at('2026-12-01T05:00:00.000Z'))).toBe(0);
    expect(incidentDays(at('2026-12-02T04:59:59.999Z'))).toBe(0);
    expect(incidentDays(at('2026-12-02T05:00:00.000Z'))).toBe(1);
    expect(incidentDays(at('2026-12-03T04:59:59.999Z'))).toBe(1);
    expect(incidentDays(at('2026-12-03T05:00:00.000Z'))).toBe(0);
  });

  it('never passes 01 between the first door and Christmas Eve', () => {
    const start = doorOpensAt(1).getTime();
    const end = Date.UTC(2026, 11, 24, 5);
    for (let t = start; t < end; t += 60 * 60 * 1000) {
      expect(incidentDays(new Date(t))).toBeLessThanOrEqual(1);
    }
  });

  it('resets on Christmas Eve 2026 and counts up from the finale', () => {
    // Before 06:00 CET it is still the station day door 12 opened on.
    expect(incidentDays(at('2026-12-24T04:59:59.999Z'))).toBe(0);
    expect(incidentDays(at('2026-12-24T05:00:00.000Z'))).toBe(0);
    expect(incidentDays(at('2026-12-25T05:00:00.000Z'))).toBe(1);
    expect(incidentDays(at('2027-01-24T05:00:00.000Z'))).toBe(31);
  });
});

describe('formatStationTime', () => {
  it('writes every door time in station time, short and lowercase', () => {
    expect(formatStationTime(doorOpensAt(1))).toBe('dec 1 · 06:00 CET');
    expect(formatStationTime(doorOpensAt(23))).toBe('dec 23 · 06:00 CET');
  });

  it('writes the first and last door day and the opening time on their own', () => {
    expect(formatStationDay(doorOpensAt(1))).toBe('dec 1');
    expect(formatStationDay(doorOpensAt(23))).toBe('dec 23');
    expect(formatStationDayLong(doorOpensAt(23))).toBe('December 23');
    expect(formatStationClock(doorOpensAt(1))).toBe('06:00 CET');
  });

  it('writes the long form for screen readers', () => {
    expect(formatStationTimeLong(doorOpensAt(1))).toBe('December 1 at 06:00 CET');
  });

  it('stays in CET in summer, when Central Europe is on daylight saving time', () => {
    expect(formatStationTime(at('2026-07-01T05:00:00.000Z'))).toBe('jul 1 · 06:00 CET');
  });
});

describe('seasonPhase', () => {
  it('is before until door 1 opens, during December, and after from the finale', () => {
    expect(seasonPhase(at('2026-12-01T04:59:59.999Z'))).toBe('before');
    expect(seasonPhase(at('2026-12-01T05:00:00.000Z'))).toBe('during');
    expect(seasonPhase(at('2026-12-24T04:59:59.999Z'))).toBe('during');
    expect(seasonPhase(at('2026-12-24T05:00:00.000Z'))).toBe('after');
  });
});
