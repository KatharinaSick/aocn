<script setup lang="ts">
// /leaderboards/<id>: a board's standings, joining or leaving it, its invite link, and its
// owner's settings. Loaded in the browser from the ID in the address (see lib/leaderboards.ts).
//
// Nothing here decides who may do what: the API answers each request as it sees fit, and the
// page only leaves out buttons that would be refused anyway. Every change is followed by a
// fresh load, so what's shown is what the API has.
import { computed, onMounted, ref } from 'vue';
import '../styles/controls.css';
import { loginHref, profileHref } from '../lib/account';
import {
  NAME_MAX,
  boardIdFromPath,
  boardMeta,
  callApi,
  cleanName,
  deadlineDropsFixes,
  endOfMinute,
  doorCells,
  formatDeadline,
  fromLocalInput,
  inviteUrl,
  playerName,
  plural,
  readBoard,
  rowKey,
  readNothing,
  toLocalInput,
  type Answer,
  type Board,
  type RemovedMember,
  type Standing,
} from '../lib/leaderboards';

type State =
  | { kind: 'loading' }
  | { kind: 'missing'; loggedIn: boolean }
  | { kind: 'unavailable' }
  | { kind: 'ready'; board: Board; now: Date };

const state = ref<State>({ kind: 'loading' });
const board = computed(() => (state.value.kind === 'ready' ? state.value.board : null));
const now = computed(() => (state.value.kind === 'ready' ? state.value.now : new Date()));

let id = '';
let path = '/';
// What the page says after a change, done or why not, next to where it was made.
type Where = 'head' | 'standings' | 'invite' | 'settings' | 'removed' | 'leave';
const message = ref<{ where: Where | ''; text: string }>({ where: '', text: '' });
const say = (where: Where, text: string) => (message.value = { where, text });
const busy = ref(false);

// Which rows show their doors in words.
const expanded = ref(new Set<string>());
// Two-step actions: what's waiting for a second click.
const confirming = ref<'' | 'leave' | 'invite' | 'delete' | 'deadline'>('');
const removing = ref<Standing | null>(null);

// The owner's settings, as edited.
const nameInput = ref('');
const deadlineInput = ref('');

async function load() {
  const answer = await callApi('GET', `/leaderboards/${id}`, readBoard);
  if (answer.state === 'ok') {
    state.value = { kind: 'ready', board: answer.value, now: new Date() };
    nameInput.value = answer.value.name;
    deadlineInput.value = answer.value.countsUntil ? toLocalInput(answer.value.countsUntil) : '';
    showTitle(answer.value.name);
  } else if (answer.state === 'missing') {
    showTitle('no leaderboard here');
    const me = await callApi('GET', '/me', (b) => (typeof b.login === 'string' ? b.login : null));
    state.value = { kind: 'missing', loggedIn: me.state === 'ok' };
  } else {
    state.value = { kind: 'unavailable' };
  }
}

/**
 * The page's title is the board's name, which only the API knows: the prerendered page says
 * "leaderboard" until it's loaded (pages/leaderboards/board.astro).
 */
function showTitle(text: string) {
  const title = document.querySelector('.textPageTitleText');
  if (title) title.textContent = text;
  document.title = `${text} · Advent of Cloud Native`;
}

onMounted(async () => {
  path = window.location.pathname;
  id = boardIdFromPath(path) ?? '';
  if (!id) {
    showTitle('no leaderboard here');
    state.value = { kind: 'missing', loggedIn: false };
    return;
  }
  await load();
});

/** Runs a change, says how it went where it was made, and loads the board again. */
async function change<T>(where: Where, request: Promise<Answer<T>>, done: string): Promise<boolean> {
  busy.value = true;
  message.value = { where: '', text: '' };
  const answer = await request;
  busy.value = false;
  confirming.value = '';
  if (answer.state === 'ok') {
    say(where, done);
    await load();
    return true;
  }
  if (answer.state === 'refused') say(where, answer.message);
  else if (answer.state === 'loggedOut') say(where, "you're logged out. Log in again, then try once more");
  else if (answer.state === 'missing') say(where, 'this leaderboard is gone');
  else say(where, "that didn't work, try again in a minute");
  return false;
}

const patch = (body: Record<string, unknown>, done: string) =>
  change('settings', callApi('PATCH', `/leaderboards/${id}`, readNothing, { body }), done);

function toggle(row: string) {
  const next = new Set(expanded.value);
  if (next.has(row)) next.delete(row);
  else next.add(row);
  expanded.value = next;
}

async function join() {
  await change('head', callApi('POST', `/leaderboards/${id}/join`, readNothing), "you're in");
}

async function leave(me: Standing) {
  busy.value = true;
  const answer = await callApi('DELETE', `/leaderboards/${id}/members/${me.id}`, readNothing);
  busy.value = false;
  if (answer.state === 'ok') {
    window.location.assign('/leaderboards');
    return;
  }
  confirming.value = '';
  say('leave', answer.state === 'refused' ? answer.message : "that didn't work, try again in a minute");
}

async function remove() {
  const who = removing.value;
  if (!who) return;
  await change(
    'standings',
    callApi('DELETE', `/leaderboards/${id}/members/${who.id}`, readNothing),
    `@${who.login} is off the leaderboard, and can't join again until you let them back in under settings`,
  );
  removing.value = null;
}

async function letBackIn(who: RemovedMember) {
  await change(
    'removed',
    callApi('DELETE', `/leaderboards/${id}/removed/${who.id}`, readNothing),
    `@${who.login} can join again. They're not back on the leaderboard until they do`,
  );
}

async function newInvite() {
  await change('invite', callApi('POST', `/leaderboards/${id}/invite`, readNothing), 'new invite link made. The old one stopped working');
}

async function rename() {
  const clean = cleanName(nameInput.value);
  if (!clean) {
    say('settings', `a name has 1 to ${NAME_MAX} characters, on one line`);
    return;
  }
  await patch({ name: clean }, 'renamed');
}

/** The deadline as entered: the end of the picked minute, or null for none or not a date. */
const newDeadline = computed(() => {
  const picked = deadlineInput.value ? fromLocalInput(deadlineInput.value) : null;
  return picked ? endOfMinute(picked) : null;
});
const deadlineDrops = computed(() => !!board.value && deadlineDropsFixes(board.value.countsUntil, newDeadline.value));

async function saveDeadline(confirmed: boolean) {
  if (deadlineInput.value && !newDeadline.value) {
    say('settings', "that deadline isn't a date and a time");
    return;
  }
  if (!confirmed && deadlineDrops.value) {
    confirming.value = 'deadline';
    return;
  }
  const until = newDeadline.value;
  await patch({ countsUntil: until ? until.toISOString() : null }, until ? `fixes count until ${formatDeadline(until)}` : 'no deadline: every fix counts');
}

async function deleteBoard() {
  busy.value = true;
  const answer = await callApi('DELETE', `/leaderboards/${id}`, readNothing);
  busy.value = false;
  if (answer.state === 'ok') {
    window.location.assign('/leaderboards');
    return;
  }
  confirming.value = '';
  say('settings', answer.state === 'refused' ? answer.message : "that didn't work, try again in a minute");
}

async function copyInvite(url: string) {
  try {
    await navigator.clipboard.writeText(url);
    say('invite', 'copied');
  } catch {
    // No clipboard: the link is right there to copy by hand.
    say('invite', 'copy the link from the field');
  }
}

const me = computed(() => board.value?.standings.find((s) => s.you) ?? null);
const invite = computed(() => (board.value?.joinCode ? inviteUrl(window.location.origin, board.value.joinCode) : ''));
const timeZone = computed(() => Intl.DateTimeFormat().resolvedOptions().timeZone);
const strips = computed(() => (board.value ? board.value.standings.map((s) => doorCells(board.value!.doors, s, now.value)) : []));
const counted = computed(() => board.value?.doors.filter((d) => d.counts).length ?? 0);
</script>

<template>
  <div class="leaderboardBoard" :data-state="state.kind">
    <p v-if="state.kind === 'loading'" class="leaderboardBoardQuiet" role="status">loading the standings…</p>

    <template v-else-if="state.kind === 'missing'">
      <p>There's no leaderboard here, or it's private and you're not in it.</p>
      <p v-if="!state.loggedIn">
        In it? <a :href="loginHref(path)">Log in with GitHub</a> to see it.
      </p>
      <p v-else>To join a private leaderboard, ask its owner for the invite link.</p>
      <p><a href="/leaderboards">your leaderboards</a></p>
    </template>

    <p v-else-if="state.kind === 'unavailable'">The standings can't be loaded right now. Try again in a few minutes.</p>

    <template v-else-if="board">
      <section class="leaderboardBoardHead leaderboardBoardText" aria-label="about this leaderboard">
        <p class="leaderboardBoardQuiet">by @{{ board.owner.login }} · {{ boardMeta(board) }}</p>
        <p v-if="board.final && board.countsUntil" class="leaderboardBoardNote">
          Final standings: only fixes up to {{ formatDeadline(board.countsUntil) }} count here. Everyone's progress goes on
          as usual everywhere else.
        </p>
        <p v-else-if="board.countsUntil" class="leaderboardBoardNote">
          Fixes count here until {{ formatDeadline(board.countsUntil) }}. Then these standings are final.
        </p>

        <div v-if="!board.you.member && board.public && board.openJoin" class="leaderboardBoardActions">
          <button v-if="board.you.loggedIn" class="button button-primary" type="button" :disabled="busy" @click="join">
            join this leaderboard
          </button>
          <a v-else class="button button-primary" :href="loginHref(path)">log in with GitHub to join</a>
          <span class="leaderboardBoardQuiet">Members see your GitHub name and your points. Anyone with this page's link sees your points too, and your name if your profile card is public.</span>
        </div>
        <p v-if="message.where === 'head'" class="leaderboardBoardMessage" role="status">{{ message.text }}</p>
      </section>

      <section class="leaderboardBoardSection" aria-labelledby="leaderboardBoardStandings">
        <h2 id="leaderboardBoardStandings">standings</h2>
        <!-- Scrolls sideways on a narrow screen; focusable, so it scrolls with the keyboard too. -->
        <div class="leaderboardBoardScroll" tabindex="0" role="region" aria-label="standings table">
          <table class="leaderboardBoardTable">
            <thead>
              <tr>
                <th scope="col" class="leaderboardBoardRank">#</th>
                <th scope="col" class="leaderboardBoardPlayer">player</th>
                <th scope="col" class="leaderboardBoardDoors">doors</th>
                <th scope="col" class="leaderboardBoardNumber">points</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="(row, i) in board.standings" :key="rowKey(row, i)">
                <tr :class="{ leaderboardBoardYou: row.you }">
                  <td class="leaderboardBoardRank">{{ row.rank }}</td>
                  <td class="leaderboardBoardPlayer">
                    <a v-if="row.profilePublic" :href="profileHref(row.login)">@{{ row.login }}</a>
                    <span v-else :class="{ leaderboardBoardQuiet: row.private }">{{ playerName(row) }}</span>
                    <span v-if="row.you" class="leaderboardBoardTag"> · you</span>
                    <span v-if="row.owner" class="leaderboardBoardTag"> · owner</span>
                  </td>
                  <td class="leaderboardBoardDoors">
                    <button
                      class="leaderboardBoardStrip"
                      type="button"
                      :aria-expanded="expanded.has(rowKey(row, i))"
                      :aria-controls="`leaderboardBoardDoors${i}`"
                      @click="toggle(rowKey(row, i))"
                    >
                      <span class="leaderboardBoardCells" aria-hidden="true">
                        <span
                          v-for="cell in strips[i]"
                          :key="cell.door"
                          :class="['leaderboardBoardCell', `leaderboardBoardCell-${cell.state}`]"
                          :title="cell.label"
                        ></span>
                      </span>
                      <span class="srOnly">{{ row.fixed }} of {{ counted }} doors fixed by {{ playerName(row) }}, details</span>
                    </button>
                  </td>
                  <td class="leaderboardBoardNumber leaderboardBoardScore">
                    <span class="leaderboardBoardPoints">{{ row.points }}</span>
                    <span v-if="row.failedVerifies > 0" class="leaderboardBoardFailed">
                      {{ plural(row.failedVerifies, 'failed verify', 'failed verifies') }}
                    </span>
                  </td>
                </tr>
                <tr v-if="expanded.has(rowKey(row, i))" :id="`leaderboardBoardDoors${i}`" class="leaderboardBoardDetails">
                  <td colspan="4">
                    <ul class="leaderboardBoardDoorList">
                      <li v-for="cell in strips[i].filter((c) => c.state !== 'closed')" :key="cell.door" :class="`leaderboardBoardDoorList-${cell.state}`">
                        {{ cell.label }}
                      </li>
                      <li v-if="strips[i].every((c) => c.state === 'closed')">no door counts here yet</li>
                    </ul>
                    <!-- Only the owner removes someone, and never themselves. -->
                    <template v-if="board.you.owner && !row.owner">
                      <div v-if="removing && removing.id === row.id" class="leaderboardBoardConfirm">
                        <p>Remove @{{ row.login }}? They can't join again, with the invite link or without, until you let them back in under settings.</p>
                        <div class="leaderboardBoardActions">
                          <button class="button button-primary" type="button" :disabled="busy" @click="remove">remove @{{ row.login }}</button>
                          <button class="button button-secondary" type="button" @click="removing = null">cancel</button>
                        </div>
                      </div>
                      <p v-else>
                        <button
                          class="button button-text"
                          type="button"
                          @click="removing = row"
                        >
                          remove @{{ row.login }}
                        </button>
                      </p>
                    </template>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>
        </div>
        <p v-if="message.where === 'standings'" class="leaderboardBoardMessage" role="status">{{ message.text }}</p>
        <p class="leaderboardBoardQuiet leaderboardBoardText">
          Together: {{ plural(board.together, 'door', 'doors') }} fixed. Same points? Fewer failed verifies is ahead.
          Select someone's doors for the details. <a href="/how-it-works#man-points">How points work</a>
        </p>
      </section>

      <section v-if="board.you.member" class="leaderboardBoardSection leaderboardBoardText" aria-labelledby="leaderboardBoardInvite">
        <h2 id="leaderboardBoardInvite">invite</h2>
        <p v-if="!board.public">Anyone with this link can join, and see the standings once they're in.</p>
        <p v-else-if="board.openJoin">Anyone can join right from this page. The link works too.</p>
        <p v-else>Anyone with this page's link can watch. Only people with the invite link can join.</p>
        <label class="textInputLabel" for="leaderboardBoardInviteLink">invite link</label>
        <div class="leaderboardBoardActions">
          <input id="leaderboardBoardInviteLink" class="textInput leaderboardBoardGrow" type="text" readonly :value="invite" @focus="($event.target as HTMLInputElement).select()" />
          <button class="button button-primary" type="button" @click="copyInvite(invite)">copy</button>
        </div>
        <p v-if="message.where === 'invite'" class="leaderboardBoardMessage" role="status">{{ message.text }}</p>
        <template v-if="board.you.owner">
          <div v-if="confirming === 'invite'" class="leaderboardBoardConfirm">
            <p>The current link stops working. Anyone who has it can't join with it anymore.</p>
            <div class="leaderboardBoardActions">
              <button class="button button-primary" type="button" :disabled="busy" @click="newInvite">make a new link</button>
              <button class="button button-secondary" type="button" @click="confirming = ''">cancel</button>
            </div>
          </div>
          <p v-else>
            <button class="button button-text" type="button" @click="confirming = 'invite'">make a new invite link</button>
          </p>
        </template>
      </section>

      <section v-if="board.you.owner" class="leaderboardBoardSection leaderboardBoardText" aria-labelledby="leaderboardBoardSettings">
        <h2 id="leaderboardBoardSettings">settings</h2>

        <form class="leaderboardBoardForm" @submit.prevent="rename">
          <label class="textInputLabel" for="leaderboardBoardNameInput">name</label>
          <div class="leaderboardBoardActions">
            <input id="leaderboardBoardNameInput" v-model="nameInput" class="textInput leaderboardBoardGrow" type="text" autocomplete="off" required />
            <button class="button button-secondary" type="submit" :disabled="busy">rename</button>
          </div>
        </form>

        <fieldset class="leaderboardBoardFieldset">
          <legend class="textInputLabel">who sees it</legend>
          <label class="leaderboardBoardCheck">
            <input type="radio" name="leaderboardBoardVisibility" :checked="!board.public" :disabled="busy" @change="patch({ public: false }, 'private: only members see it')" />
            private: only members see it
          </label>
          <label class="leaderboardBoardCheck">
            <input type="radio" name="leaderboardBoardVisibility" :checked="board.public" :disabled="busy" @change="patch({ public: true }, 'public: anyone with the link sees it')" />
            public: anyone with the link sees it
          </label>
        </fieldset>

        <fieldset v-if="board.public" class="leaderboardBoardFieldset">
          <legend class="textInputLabel">who can join</legend>
          <label class="leaderboardBoardCheck">
            <input type="radio" name="leaderboardBoardJoin" :checked="!board.openJoin" :disabled="busy" @change="patch({ openJoin: false }, 'invite only: joining takes the invite link')" />
            invite only: joining takes the invite link
          </label>
          <label class="leaderboardBoardCheck">
            <input type="radio" name="leaderboardBoardJoin" :checked="board.openJoin" :disabled="busy" @change="patch({ openJoin: true }, 'open: anyone who sees it can join')" />
            open: anyone who sees it can join
          </label>
        </fieldset>

        <form class="leaderboardBoardForm" @submit.prevent="saveDeadline(false)">
          <label class="textInputLabel" for="leaderboardBoardDeadline">counts until (optional, in your time zone: {{ timeZone }})</label>
          <div class="leaderboardBoardActions">
            <input id="leaderboardBoardDeadline" v-model="deadlineInput" class="textInput" type="datetime-local" @input="confirming = ''" />
            <button class="button button-secondary" type="submit" :disabled="busy">save deadline</button>
            <button
              v-if="board.countsUntil"
              class="button button-text"
              type="button"
              :disabled="busy"
              @click="
                deadlineInput = '';
                saveDeadline(true);
              "
            >
              remove deadline
            </button>
          </div>
          <p class="leaderboardBoardQuiet">Only fixes up to the deadline count here. Without one, every fix counts, whenever it's made.</p>
          <div v-if="confirming === 'deadline' && newDeadline" class="leaderboardBoardConfirm">
            <p>Fixes after {{ formatDeadline(newDeadline) }} will stop counting on this leaderboard.</p>
            <div class="leaderboardBoardActions">
              <button class="button button-primary" type="button" :disabled="busy" @click="saveDeadline(true)">save anyway</button>
              <button class="button button-secondary" type="button" @click="confirming = ''">cancel</button>
            </div>
          </div>
        </form>

        <p v-if="message.where === 'settings'" class="leaderboardBoardMessage" role="status">{{ message.text }}</p>

        <div class="leaderboardBoardForm">
          <h3 class="textInputLabel">removed</h3>
          <ul v-if="board.removed.length > 0" class="leaderboardBoardRemoved">
            <li v-for="who in board.removed" :key="who.id" class="leaderboardBoardActions">
              <span class="leaderboardBoardGrow">@{{ who.login }}</span>
              <button class="button button-text" type="button" :disabled="busy" @click="letBackIn(who)">let @{{ who.login }} back in</button>
            </li>
          </ul>
          <p v-else class="leaderboardBoardQuiet">Nobody. Whoever you remove can't join again until you let them back in here.</p>
          <p v-if="message.where === 'removed'" class="leaderboardBoardMessage" role="status">{{ message.text }}</p>
        </div>

        <div v-if="confirming === 'delete'" class="leaderboardBoardConfirm">
          <p>Delete {{ board.name }} for everyone? This can't be undone. Everyone's progress stays, on their card and on other leaderboards.</p>
          <div class="leaderboardBoardActions">
            <button class="button button-primary" type="button" :disabled="busy" @click="deleteBoard">delete it</button>
            <button class="button button-secondary" type="button" @click="confirming = ''">cancel</button>
          </div>
        </div>
        <p v-else>
          <button class="button button-text" type="button" @click="confirming = 'delete'">delete this leaderboard</button>
        </p>
      </section>

      <section v-else-if="board.you.member && me" class="leaderboardBoardSection leaderboardBoardText" aria-labelledby="leaderboardBoardLeave">
        <h2 id="leaderboardBoardLeave">leave</h2>
        <div v-if="confirming === 'leave'" class="leaderboardBoardConfirm">
          <p>Leave {{ board.name }}? You can rejoin with the invite link.</p>
          <div class="leaderboardBoardActions">
            <button class="button button-primary" type="button" :disabled="busy" @click="leave(me)">leave</button>
            <button class="button button-secondary" type="button" @click="confirming = ''">cancel</button>
          </div>
        </div>
        <p v-else>
          <button class="button button-text" type="button" @click="confirming = 'leave'">leave this leaderboard</button>
        </p>
        <p v-if="message.where === 'leave'" class="leaderboardBoardMessage" role="status">{{ message.text }}</p>
      </section>
    </template>
  </div>
</template>

<style scoped>
.leaderboardBoard {
  display: grid;
  gap: var(--space-8);
  min-width: 0;
}

.leaderboardBoardHead,
.leaderboardBoardSection,
.leaderboardBoardForm {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

/* The page is wide for the standings; text stays in a readable column. */
.leaderboardBoardText {
  max-width: 72ch;
}

.leaderboardBoardQuiet {
  color: var(--color-text-soft);
  font-size: var(--font-size-small);
  line-height: var(--line-height-small);
}

.leaderboardBoardNote {
  color: var(--color-text);
}

.leaderboardBoardActions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.leaderboardBoardGrow {
  flex: 1 1 20ch;
  min-width: 0;
}

.leaderboardBoardScroll {
  overflow-x: auto;
  border: 1px solid var(--color-line-strong);
  border-radius: var(--radius-screen);
  background: var(--color-panel);
}

.leaderboardBoardTable {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-size-small);
  line-height: var(--line-height-small);
}

.leaderboardBoardTable th {
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-line-strong);
  color: var(--color-text-soft);
  font-weight: var(--font-weight-regular);
  text-align: left;
  vertical-align: bottom;
  white-space: nowrap;
}

.leaderboardBoardTable td {
  padding: var(--space-2) var(--space-4);
  border-bottom: 1px solid var(--color-line);
  color: var(--color-text);
  white-space: nowrap;
}

.leaderboardBoardTable .leaderboardBoardNumber {
  width: 1%;
  text-align: right;
}

.leaderboardBoardTable .leaderboardBoardRank {
  width: 1%;
}

/* The doors take the room that's left, with the strip in its middle. */
.leaderboardBoardTable .leaderboardBoardDoors {
  width: 100%;
  text-align: center;
}

.leaderboardBoardPoints {
  color: var(--color-text);
  font-size: var(--font-size-body);
  font-weight: var(--font-weight-bold);
}

/* Next to the points, quietly: they only break ties. */
.leaderboardBoardFailed {
  display: block;
  color: var(--color-text-soft);
}

/* Your row: marked by the "you" tag too, not by color alone. */
.leaderboardBoardYou td {
  background: var(--color-panel-new);
}

.leaderboardBoardYou td:first-child {
  box-shadow: inset 2px 0 0 var(--color-aurora);
}

.leaderboardBoardPlayer a {
  color: var(--color-text);
}

.leaderboardBoardTag {
  color: var(--color-text-soft);
}

.leaderboardBoardStrip:hover .leaderboardBoardCell-open {
  border-color: var(--color-text-soft);
}

.leaderboardBoardStrip {
  display: inline-flex;
  align-items: center;
  min-height: var(--size-touch);
  padding: 0;
  border: none;
  background: none;
  color: var(--color-text-soft);
  font: inherit;
  cursor: pointer;
}

.leaderboardBoardCells {
  display: inline-flex;
  gap: var(--space-1);
}

.leaderboardBoardCell {
  width: var(--space-6);
  height: var(--space-4);
  border: 1px solid var(--color-dim);
}

.leaderboardBoardCell-fixed {
  border-color: var(--color-aurora);
  background: var(--color-aurora);
}

.leaderboardBoardCell-closed {
  border-style: dashed;
  border-color: var(--color-line-strong);
}

.leaderboardBoardDetails td {
  white-space: normal;
  background: var(--color-bg);
}

/* Two classes, so this wins over TextPage's lists. */
.leaderboardBoardDetails .leaderboardBoardDoorList {
  display: grid;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
  color: var(--color-text-soft);
}

.leaderboardBoardDoorList-fixed {
  color: var(--color-aurora);
}

.leaderboardBoardConfirm {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
  border: 1px solid var(--color-signal);
  border-radius: var(--radius-screen);
  background: var(--color-panel);
}

.leaderboardBoardFieldset {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  border: none;
}

.leaderboardBoardRemoved {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.leaderboardBoardCheck {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.leaderboardBoardCheck input {
  accent-color: var(--color-aurora);
}

/* Phones: each player is a block, with their name and points on top and the strip under
   them, so nothing scrolls sideways. The header row is there for screen readers only. */
@media (max-width: 799px) {
  .leaderboardBoardTable thead {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }

  .leaderboardBoardTable,
  .leaderboardBoardTable tbody,
  .leaderboardBoardDetails,
  .leaderboardBoardDetails td {
    display: block;
  }

  .leaderboardBoardTable tbody tr:not(.leaderboardBoardDetails) {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    grid-template-areas:
      'rank player score'
      'doors doors doors';
    align-items: baseline;
    border-bottom: 1px solid var(--color-line);
  }

  .leaderboardBoardTable tbody td {
    border-bottom: none;
  }

  .leaderboardBoardTable .leaderboardBoardRank {
    grid-area: rank;
    width: auto;
  }

  .leaderboardBoardTable .leaderboardBoardPlayer {
    grid-area: player;
    width: auto;
    white-space: normal;
    overflow-wrap: anywhere;
  }

  .leaderboardBoardTable .leaderboardBoardScore {
    grid-area: score;
    width: auto;
  }

  .leaderboardBoardTable .leaderboardBoardDoors {
    grid-area: doors;
    width: auto;
    padding-top: 0;
  }

  .leaderboardBoardStrip,
  .leaderboardBoardDoors .leaderboardBoardCells {
    width: 100%;
  }

  .leaderboardBoardDoors .leaderboardBoardCell {
    flex: 1;
    width: auto;
  }
}

.leaderboardBoardMessage {
  color: var(--color-aurora);
  font-size: var(--font-size-small);
  line-height: var(--line-height-small);
}

.leaderboardBoardMessage:empty {
  display: none;
}
</style>
