-- Forward-only: removing the identity could reuse a revision for other bytes.
DO $$
BEGIN
    RAISE EXCEPTION 'transcript content revision migration is forward-only; retain the revision identity columns';
END
$$;
