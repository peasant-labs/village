-- Forward-only: a down run must not erase the record of a privileged content repair.
DO $$
BEGIN
    RAISE EXCEPTION 'transcript content repair migration is forward-only; retain the repair ledger';
END
$$;
