-- Forward-only: a down run must not erase a deleted transcript's last audience.
DO $$
BEGIN
    RAISE EXCEPTION 'transcript absentee state migration is forward-only; retain retraction audiences';
END
$$;
