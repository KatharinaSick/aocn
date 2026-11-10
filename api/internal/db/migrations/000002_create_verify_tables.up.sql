-- Everyone who ever sent a result, by GitHub user ID. Login and avatar are refreshed on every
-- result, since a login can be renamed and then taken by someone else; the ID can't.
CREATE TABLE users (
    github_user_id BIGINT      PRIMARY KEY,
    login          TEXT        NOT NULL,
    avatar_url     TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL
);

-- Every aocn verify, passed or not, with the API's time. Every attempt counts once there's
-- scoring, and results from before a door opened (previewers) can be left out by time.
-- checks holds each check's ID, whether it passed and what it got.
CREATE TABLE verify_results (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    github_user_id BIGINT      NOT NULL REFERENCES users,
    door           INTEGER     NOT NULL,
    passed         BOOLEAN     NOT NULL,
    checks         JSONB       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX verify_results_github_user_id_door ON verify_results (github_user_id, door);
