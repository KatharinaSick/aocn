-- Whether someone's profile card is shown to everyone. Private until they say otherwise:
-- playing doesn't mean wanting it known.
ALTER TABLE users ADD COLUMN profile_public BOOLEAN NOT NULL DEFAULT false;
