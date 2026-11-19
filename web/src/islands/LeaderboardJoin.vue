<script setup lang="ts">
// /join/<code>: what an invite link is for, who will see what, and joining. Loaded in the
// browser from the code in the address (see lib/leaderboards.ts).
import { onMounted, ref } from 'vue';
import '../styles/controls.css';
import { loginHref } from '../lib/account';
import { boardHref, callApi, formatDeadline, joinCodeFromPath, plural, readJoinPreview, type JoinPreview } from '../lib/leaderboards';

type State =
  | { kind: 'loading' }
  | { kind: 'invalid' }
  | { kind: 'unavailable' }
  | { kind: 'ready'; preview: JoinPreview; loggedIn: boolean };

const state = ref<State>({ kind: 'loading' });
const joining = ref(false);
const message = ref('');
let code = '';
let path = '/';

onMounted(async () => {
  path = window.location.pathname;
  code = joinCodeFromPath(path) ?? '';
  if (!code) {
    state.value = { kind: 'invalid' };
    return;
  }
  const [answer, me] = await Promise.all([
    callApi('GET', `/join/${code}`, readJoinPreview),
    callApi('GET', '/me', (b) => (typeof b.login === 'string' ? b.login : null)),
  ]);
  if (answer.state === 'ok') {
    state.value = { kind: 'ready', preview: answer.value, loggedIn: me.state === 'ok' };
    document.title = `join ${answer.value.name} · Advent of Cloud Native`;
  } else if (answer.state === 'missing') {
    state.value = { kind: 'invalid' };
  } else {
    state.value = { kind: 'unavailable' };
  }
});

async function join() {
  joining.value = true;
  message.value = '';
  const answer = await callApi('POST', `/join/${code}`, (b) => (typeof b.id === 'string' ? b.id : null));
  if (answer.state === 'ok') {
    window.location.assign(boardHref(answer.value));
    return;
  }
  joining.value = false;
  if (answer.state === 'refused' || answer.state === 'missing') message.value = answer.message || "this invite link isn't valid anymore";
  else if (answer.state === 'loggedOut') message.value = "you're logged out. Log in again, then try once more";
  else message.value = "that didn't work, try again in a minute";
}
</script>

<template>
  <div class="leaderboardJoin" :data-state="state.kind">
    <p v-if="state.kind === 'loading'" class="leaderboardJoinQuiet" role="status">reading the invite link…</p>

    <template v-else-if="state.kind === 'invalid'">
      <p>This invite link isn't valid anymore. Its owner may have made a new one, or deleted the leaderboard.</p>
      <p>Ask whoever sent it for the current link.</p>
    </template>

    <p v-else-if="state.kind === 'unavailable'">This invite link can't be read right now. Try again in a few minutes.</p>

    <template v-else-if="state.kind === 'ready'">
      <section class="leaderboardJoinBoard" aria-labelledby="leaderboardJoinName">
        <h2 id="leaderboardJoinName" class="leaderboardJoinName">{{ state.preview.name }}</h2>
        <p class="leaderboardJoinQuiet">
          by @{{ state.preview.owner.login }} · {{ plural(state.preview.members, 'member', 'members') }} ·
          {{ state.preview.public ? 'public' : 'private' }}
        </p>
        <p v-if="state.preview.countsUntil && state.preview.final">
          Its final standings are in: only fixes up to {{ formatDeadline(state.preview.countsUntil) }} counted. You can still
          join and look.
        </p>
        <p v-else-if="state.preview.countsUntil">
          Fixes count here until {{ formatDeadline(state.preview.countsUntil) }}. After that, its standings are final.
        </p>
        <p v-else>Every fix counts here, whenever you make it.</p>
      </section>

      <template v-if="state.preview.member">
        <p>You're in it already.</p>
        <p v-if="state.preview.id">
          <a class="button button-primary" :href="boardHref(state.preview.id)">see the standings</a>
        </p>
      </template>
      <p v-else-if="state.preview.full">
        It's full: a leaderboard has room for {{ state.preview.maxMembers }} players.
      </p>
      <template v-else>
        <p class="leaderboardJoinPrivacy">
          Members see your GitHub name and your points.
          <template v-if="state.preview.public">It's public, so anyone with its link sees them too.</template>
          <template v-else>The owner can make this leaderboard public.</template>
          You can leave any time.
        </p>
        <p v-if="state.loggedIn">
          <button class="button button-primary" type="button" :disabled="joining" @click="join">join {{ state.preview.name }}</button>
        </p>
        <p v-else>
          <a class="button button-primary" :href="loginHref(path)">log in with GitHub to join</a>
        </p>
        <p class="leaderboardJoinMessage" role="status">{{ message }}</p>
      </template>
    </template>
  </div>
</template>

<style scoped>
.leaderboardJoin {
  display: grid;
  gap: var(--space-4);
}

.leaderboardJoinBoard {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-6);
  border: 1px solid var(--color-line-strong);
  border-radius: var(--radius-screen);
  background: var(--color-panel);
}

/* Two classes, so this wins over TextPage's section headings. */
.leaderboardJoinBoard .leaderboardJoinName {
  margin: 0;
  padding: 0;
  border: none;
  color: var(--color-aurora);
  text-transform: none;
  letter-spacing: normal;
  font-size: var(--font-size-lead);
  line-height: var(--line-height-lead);
  overflow-wrap: anywhere;
}

.leaderboardJoinQuiet,
.leaderboardJoinPrivacy {
  color: var(--color-text-soft);
  font-size: var(--font-size-small);
  line-height: var(--line-height-small);
}

.leaderboardJoinMessage {
  color: var(--color-aurora);
  font-size: var(--font-size-small);
  line-height: var(--line-height-small);
}

.leaderboardJoinMessage:empty {
  display: none;
}
</style>
