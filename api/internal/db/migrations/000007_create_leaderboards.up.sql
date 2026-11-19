-- Leaderboards players make for their own groups. There's no global one, on purpose.
-- Standings are worked out from verify_results and hint_views on every read, never stored.
CREATE TABLE leaderboards (
    -- Random, for the URL. Not the join code: the link to a public board doesn't let anyone in.
    id           TEXT        PRIMARY KEY,
    owner_id     BIGINT      NOT NULL REFERENCES users,
    name         TEXT        NOT NULL,
    -- Private boards are only shown to their members, public ones to anyone with the link.
    public       BOOLEAN     NOT NULL DEFAULT false,
    -- Only means anything while public: anyone who sees the board may join it.
    open_join    BOOLEAN     NOT NULL DEFAULT false,
    -- Random, replaced by "new invite link", which makes the old link stop working.
    join_code    TEXT        NOT NULL UNIQUE,
    -- Optional deadline: only fixes up to it count on this board.
    counts_until TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL
);

-- Counting a player's own boards, for the limit.
CREATE INDEX leaderboards_owner_id ON leaderboards (owner_id);

CREATE TABLE leaderboard_members (
    leaderboard_id TEXT        NOT NULL REFERENCES leaderboards ON DELETE CASCADE,
    github_user_id BIGINT      NOT NULL REFERENCES users,
    joined_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (leaderboard_id, github_user_id)
);

-- A player's boards, for their list and their card.
CREATE INDEX leaderboard_members_github_user_id ON leaderboard_members (github_user_id);
