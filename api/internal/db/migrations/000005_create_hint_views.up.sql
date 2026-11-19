-- Each hint a player opened with aocn hint, once: opening it again is free. Hints come in
-- order, so a door's rows for a player are 0, 1, 2, … with no gaps. Points are worked out
-- from these and verify_results on every read, never stored.
CREATE TABLE hint_views (
    github_user_id BIGINT      NOT NULL REFERENCES users,
    door           INTEGER     NOT NULL,
    hint           INTEGER     NOT NULL CHECK (hint >= 0),
    created_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (github_user_id, door, hint)
);
