-- Whether a failed run stopped at a ready check: the workloads weren't up yet, typically
-- right after aocn open. Those runs never count as failed verifies. Set by the API from the
-- door's checks; runs from before this column stopped where aocn marked the rest "not run".
ALTER TABLE verify_results ADD COLUMN not_ready BOOLEAN NOT NULL DEFAULT false;
UPDATE verify_results SET not_ready = true
WHERE NOT passed AND checks @> '[{"got": "not run"}]';
