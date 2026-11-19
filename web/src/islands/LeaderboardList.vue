<script setup lang="ts">
// /leaderboards: the boards of who is logged in, and making a new one. Loaded in the browser,
// so the page itself is the same for everyone (see lib/leaderboards.ts).
import { computed, onMounted, ref } from 'vue';
import '../styles/controls.css';
import { loginHref } from '../lib/account';
import { NAME_MAX, boardHref, callApi, cleanName, rankText, readMyBoards, type MyBoards } from '../lib/leaderboards';

type State = { kind: 'loading' } | { kind: 'loggedOut' } | { kind: 'unavailable' } | { kind: 'ready'; mine: MyBoards };

const state = ref<State>({ kind: 'loading' });
const name = ref('');
const creating = ref(false);
const message = ref('');

const mine = computed(() => (state.value.kind === 'ready' ? state.value.mine : null));
const canCreate = computed(() => !!mine.value && mine.value.owned < mine.value.maxOwned);

onMounted(async () => {
  const answer = await callApi('GET', '/me/leaderboards', readMyBoards);
  if (answer.state === 'ok') state.value = { kind: 'ready', mine: answer.value };
  else if (answer.state === 'loggedOut') state.value = { kind: 'loggedOut' };
  else state.value = { kind: 'unavailable' };
});

async function create() {
  const clean = cleanName(name.value);
  if (!clean) {
    message.value = `a name has 1 to ${NAME_MAX} characters, on one line`;
    return;
  }
  creating.value = true;
  message.value = '';
  const answer = await callApi('POST', '/me/leaderboards', (b) => (typeof b.id === 'string' ? b.id : null), { body: { name: clean } });
  if (answer.state === 'ok') {
    window.location.assign(boardHref(answer.value));
    return;
  }
  creating.value = false;
  if (answer.state === 'refused') message.value = answer.message;
  else if (answer.state === 'loggedOut') message.value = "you're logged out. Log in again, then try once more";
  else message.value = "that didn't work, try again in a minute";
}
</script>

<template>
  <div class="leaderboardList" :data-state="state.kind">
    <p v-if="state.kind === 'loading'" class="leaderboardListQuiet" role="status">loading your leaderboards…</p>

    <template v-else-if="state.kind === 'loggedOut'">
      <p>Log in with GitHub to see your leaderboards, or to make one for your team.</p>
      <p>
        <a class="button button-primary" :href="loginHref('/leaderboards')">log in with GitHub</a>
      </p>
    </template>

    <p v-else-if="state.kind === 'unavailable'">Your leaderboards can't be loaded right now. Try again in a few minutes.</p>

    <template v-else-if="mine">
      <section class="leaderboardListSection" aria-labelledby="leaderboardListYours">
        <h2 id="leaderboardListYours">yours</h2>
        <ul v-if="mine.boards.length" class="leaderboardListBoards">
          <li v-for="board in mine.boards" :key="board.id" class="leaderboardListBoard">
            <a class="leaderboardListName" :href="boardHref(board.id)">{{ board.name }}</a>
            <span class="leaderboardListMeta">
              {{ rankText(board.rank, board.members) }} · {{ board.public ? 'public' : 'private'
              }}<template v-if="board.own"> · yours</template><template v-if="board.final"> · final</template>
            </span>
          </li>
        </ul>
        <p v-else>No leaderboards yet. Make one for your team below, or ask whoever has one for its invite link.</p>
      </section>

      <section class="leaderboardListSection" aria-labelledby="leaderboardListMake">
        <h2 id="leaderboardListMake">make one</h2>
        <p v-if="!canCreate">
          You have {{ mine.maxOwned }} leaderboards of your own, the most there can be. Delete one to make another.
        </p>
        <form v-else class="leaderboardListForm" @submit.prevent="create">
          <label class="textInputLabel" for="leaderboardListNameInput">name</label>
          <div class="leaderboardListRow">
            <input
              id="leaderboardListNameInput"
              v-model="name"
              class="textInput leaderboardListInput"
              type="text"
              autocomplete="off"
              required
              placeholder="Platform Team"
            />
            <button class="button button-primary" type="submit" :disabled="creating">create</button>
          </div>
          <p class="leaderboardListQuiet">
            It starts private: only the people you send its invite link to can see it. You can have
            {{ mine.maxOwned }} of your own, and join as many as you like.
          </p>
        </form>
        <p class="leaderboardListMessage" role="status">{{ message }}</p>
      </section>
    </template>
  </div>
</template>

<style scoped>
.leaderboardList {
  display: grid;
  gap: var(--space-8);
}

.leaderboardListSection {
  display: grid;
  gap: var(--space-4);
}

/* Two classes, so this wins over TextPage's lists. */
.leaderboardList .leaderboardListBoards {
  display: grid;
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}

.leaderboardListBoard {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2) var(--space-4);
  padding: var(--space-4);
  border: 1px solid var(--color-line-strong);
  border-radius: var(--radius-screen);
  background: var(--color-panel);
}

.leaderboardListName {
  color: var(--color-text);
  font-weight: var(--font-weight-bold);
  overflow-wrap: anywhere;
}

.leaderboardListMeta,
.leaderboardListQuiet {
  color: var(--color-text-soft);
  font-size: var(--font-size-small);
  line-height: var(--line-height-small);
}

.leaderboardListForm {
  display: grid;
  gap: var(--space-2);
}

.leaderboardListRow {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.leaderboardListInput {
  flex: 1 1 16ch;
  min-width: 0;
}

.leaderboardListMessage {
  color: var(--color-aurora);
  font-size: var(--font-size-small);
  line-height: var(--line-height-small);
}

.leaderboardListMessage:empty {
  display: none;
}
</style>
