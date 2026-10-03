-- users.auto_attach_pull_requests
--
-- The author's choice to link their own transcripts to a pull request when it
-- opens in a repository one of their collectives links. It is off by default,
-- and it is separate from preview_before_attach, which asks before each attach:
-- one flag for both would merge two different choices.
ALTER TABLE users
    ADD COLUMN auto_attach_pull_requests BOOLEAN NOT NULL DEFAULT false;
