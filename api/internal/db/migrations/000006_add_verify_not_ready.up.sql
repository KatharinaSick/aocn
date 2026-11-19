-- Whether a failed run stopped at a ready check: the workloads weren't up yet, typically
-- right after aocn open. Those runs never count as failed verifies. Set by the API from the
-- door's checks: the first check that failed is a ready check. Runs from before this column
-- count as not ready where aocn marked checks "not run", which only follow a failed ready
-- check. The two agree as long as a door's ready checks come first, as door 1's does.
ALTER TABLE verify_results ADD COLUMN not_ready BOOLEAN NOT NULL DEFAULT false;
UPDATE verify_results SET not_ready = true
WHERE NOT passed AND checks @> '[{"got": "not run"}]';
