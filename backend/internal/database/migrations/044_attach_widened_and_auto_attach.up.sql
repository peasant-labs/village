-- Two facts the pull request attachment needs and cannot derive.
--
-- 1. pull_request_attachment_transcripts.attach_widened
--
-- Attaching a transcript to a pull request used to widen it: a public repository
-- raised it to public, and a private one opened an approved share to the linking
-- collective. Attaching now binds only and never changes who can read a
-- transcript. Detach must still undo what the old attach did, and must touch
-- nothing for a binding made since.
--
-- previous_visibility cannot tell the two apart. It records the transcript's
-- visibility before the binding under both rules, so an old binding that raised
-- a private transcript to public and a new binding whose owner later made the
-- same transcript public by hand carry the same row and the same current value.
-- Nothing else in the database separates them either: the binding row has no
-- timestamp to match against the audit event the old widening wrote, and the
-- share the old attach opened is written by the same statement as an owner's own.
--
-- So the binding records it. Every row that exists when this migration runs was
-- written by the old attach, and the constant default marks each of them true
-- without rewriting the table. The default is then dropped, so every later
-- insert must state the value: a writer that forgot it fails loudly instead of
-- quietly marking a new binding as one detach must narrow.
ALTER TABLE pull_request_attachment_transcripts
    ADD COLUMN attach_widened BOOLEAN NOT NULL DEFAULT true;

ALTER TABLE pull_request_attachment_transcripts
    ALTER COLUMN attach_widened DROP DEFAULT;

-- 2. users.auto_attach_pull_requests
--
-- The author's choice to link their own transcripts to a pull request when it
-- opens in a repository one of their collectives links. It is off by default,
-- and it is separate from preview_before_attach, which asks before each attach:
-- one flag for both would merge two different choices.
ALTER TABLE users
    ADD COLUMN auto_attach_pull_requests BOOLEAN NOT NULL DEFAULT false;
