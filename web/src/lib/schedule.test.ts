import { readdirSync, readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { doorsOf } from './doors';
import { parseSchedule } from './schedule';

// The same files as api/internal/schedule/schedule_test.go reads.
const repo = new URL('../../../', import.meta.url);
const read = (path: string) => readFileSync(new URL(path, repo), 'utf8');

const SECOND = 1000;
const iso = (ms: number) => new Date(ms).toISOString();

describe('the test schedule', () => {
  const fixture = parseSchedule(read('testdata/schedule.yaml'));
  const doors = doorsOf(fixture);

  // Written out in the Go test too: both sides must agree on every moment.
  it('opens every door at the same moment in UTC as the API', () => {
    expect(fixture.doors.map(({ door, opensAt }) => [door, iso(opensAt)])).toEqual([
      [1, '2030-12-01T05:00:00.000Z'],
      [3, '2030-12-03T05:00:00.000Z'],
      [5, '2030-12-05T00:30:00.000Z'],
    ]);
    expect(iso(fixture.christmasEve.beforeSeason)).toBe('2029-12-24T05:00:00.000Z');
    expect(iso(fixture.christmasEve.finale)).toBe('2030-12-24T05:00:00.000Z');
  });

  it('keeps door 1 closed until the second it opens', () => {
    const opensAt = fixture.doors[0].opensAt;
    expect(doors.isDoorOpen(1, new Date(opensAt - SECOND))).toBe(false);
    expect(doors.nextDoor(new Date(opensAt - SECOND))).toEqual({ door: 1, opensAt: new Date(opensAt) });
    expect(doors.isDoorOpen(1, new Date(opensAt))).toBe(true);
    expect(doors.nextDoor(new Date(opensAt))?.door).toBe(3);
  });

  it('has every door open and none to come after the last door', () => {
    const now = new Date(fixture.doors[2].opensAt);
    for (const door of doors.numbers) expect(doors.isDoorOpen(door, now)).toBe(true);
    expect(doors.nextDoor(now)).toBeNull();
  });
});

describe('schedule.yaml', () => {
  const schedule = parseSchedule(read('schedule.yaml'));

  it('has twelve doors', () => {
    expect(schedule.doors).toHaveLength(12);
  });

  it('opens the doors in order', () => {
    for (let i = 1; i < schedule.doors.length; i++) {
      expect(schedule.doors[i].opensAt).toBeGreaterThan(schedule.doors[i - 1].opensAt);
    }
  });

  it('opens every door on the minute', () => {
    for (const { opensAt } of schedule.doors) expect(opensAt % (60 * SECOND)).toBe(0);
  });

  // Only for the real schedule: a rehearsal may move a date without renumbering the door.
  it('numbers every door by its day in December, in station time', () => {
    for (const { door, opensAt } of schedule.doors) {
      const cet = new Date(opensAt + 60 * 60 * SECOND);
      expect([cet.getUTCMonth(), cet.getUTCDate()]).toEqual([11, door]);
    }
  });

  it('counts incidents from before door 1 until the finale after the last door', () => {
    expect(schedule.christmasEve.beforeSeason).toBeLessThan(schedule.doors[0].opensAt);
    expect(schedule.christmasEve.finale).toBeGreaterThan(schedule.doors[schedule.doors.length - 1].opensAt);
  });
});

describe('an unusual but valid schedule', () => {
  const dir = new URL('testdata/schedule-valid/', repo);

  // Each file lists on its first line the UTC moment of every door; the Go test reads the same.
  it.each(readdirSync(dir).filter((name) => name.endsWith('.yaml')))('accepts %s', (name) => {
    const text = readFileSync(new URL(name, dir), 'utf8');
    const expected = /^# utc: (.*)$/m.exec(text)?.[1].split(' ');
    expect(expected).toBeTruthy();
    expect(parseSchedule(text).doors.map((d) => d.opensAt)).toEqual(expected?.map((utc) => new Date(utc).getTime()));
  });
});

describe('a broken schedule', () => {
  const dir = new URL('testdata/schedule-broken/', repo);

  // Each file says on its first line what the error must contain; the Go test reads the same.
  it.each(readdirSync(dir).filter((name) => name.endsWith('.yaml')))('rejects %s', (name) => {
    const text = readFileSync(new URL(name, dir), 'utf8');
    const expected = /^# error: (.*)$/m.exec(text)?.[1];
    expect(expected).toBeTruthy();
    expect(() => parseSchedule(text)).toThrow(expected);
  });

  it('rejects a file that is not YAML', () => {
    expect(() => parseSchedule('doors: [')).toThrow('not valid YAML');
  });
});
