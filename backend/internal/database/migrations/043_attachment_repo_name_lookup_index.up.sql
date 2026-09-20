-- The publish hook finds one author's attachments for a repository — a waiting
-- request it can complete, or an attached one it can refresh — comparing the
-- repository NAME and never the owner. That is the matcher's rule too:
-- reponame.NormalizeRemote reduces a remote to its last path segment, and a fork's
-- clone keeps that name while its owner changes, so the name is what lets a fork
-- push match its attachment at all.
--
-- The predicate is (author_id, lower(repo_name), state). The table's other index
-- leads with lower(repo_owner), so it cannot filter by the name; the planner falls
-- back to (author_id, state) and filters, on a table that only grows. This index
-- gives the name a leading position beside the author.
--
-- state is deliberately not a column. The predicate says `state = ANY(...)`, which
-- Postgres plans as a filter rather than an index condition, so a third column
-- would cost writes and buy nothing: measured on a 400k-row table the plan and its
-- cost are identical with and without it, and one (author, name) pair holds few
-- enough rows that filtering a single state over them is as cheap.
CREATE INDEX idx_pull_request_attachments_author_repo_name
    ON pull_request_attachments (author_id, lower(repo_name));
