-- Who a leaderboard's owner removed: they can't join it again, with the invite link or
-- without, until the owner takes them off this list. Leaving on your own puts nobody here.
CREATE TABLE leaderboard_bans (
    leaderboard_id TEXT        NOT NULL REFERENCES leaderboards ON DELETE CASCADE,
    github_user_id BIGINT      NOT NULL REFERENCES users,
    created_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (leaderboard_id, github_user_id)
);
