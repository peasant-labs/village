-- The publish hook finds one author's attachments for a repository — a waiting
-- request it can complete, or an attached one it can refresh — comparing the
-- repository NAME and never the owner. That comparison is the matcher's rule too:
-- reponame.NormalizeRemote reduces a remote to its last path segment, and a fork's
-- clone keeps that name while its owner changes, so the name is what lets a fork
-- push match its attachment at all.
--
-- The predicate is (author_id, lower(repo_name), state). The table's other index
-- leads with lower(repo_owner), which this predicate never constrains, so the
-- planner has nothing to use and filters instead, on a table that only grows.
-- This index serves the predicate as written.
CREATE INDEX idx_pull_request_attachments_author_repo_name_state
    ON pull_request_attachments (author_id, lower(repo_name), state);
