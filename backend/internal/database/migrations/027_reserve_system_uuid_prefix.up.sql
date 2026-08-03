-- Reserve the first-80-bits-zero UUID range (00000000-0000-0000-0000-*) for
-- SYSTEM identities and fence users.id out of it.
--
-- The governance audit attributes non-user mutations to the reserved system
-- actor 00000000-0000-0000-0000-000000000000 (database.SystemActorID). If a user
-- could ever be minted with an id in that range, a person could collide with the
-- system-actor attribution — forging system actions and breaking "show me every
-- system action" filtering. This CHECK makes the reservation unforgeable at the
-- storage boundary: it fences EVERY insert path (the app default, raw-SQL seeds,
-- and any future explicit/external-id import), independent of any single Go call
-- site.
--
-- No existing user id is in the reserved prefix, so the constraint validates
-- cleanly against current data: the app never supplies an id and relies on the
-- gen_random_uuid() default (v4, version nibble 4, structurally outside the
-- prefix), and the only explicit-id path (scripts/seed.sql) uses a0000000-* ids.
ALTER TABLE users
    ADD CONSTRAINT users_id_not_system_reserved
    CHECK (id::text NOT LIKE '00000000-0000-0000-0000-%');
