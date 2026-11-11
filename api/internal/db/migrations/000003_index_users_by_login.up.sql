-- Profile cards look users up by login, as people type it: GitHub logins ignore case. A login
-- can be renamed and taken by someone else, so several rows may share one; the newest wins.
CREATE INDEX users_lower_login ON users (lower(login), updated_at DESC);
