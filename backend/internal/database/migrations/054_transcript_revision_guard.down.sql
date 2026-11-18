-- Forward-only: dropping the guard would let a replacement change content without counting a revision.
DO $$
BEGIN
    RAISE EXCEPTION 'transcript revision guard migration is forward-only; retain the transition guard';
END
$$;
