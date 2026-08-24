-- wly's database. Applied by Postgres on first start from
-- /docker-entrypoint-initdb.d, and safe to re-run by hand.
--
-- WHAT LIVES HERE, AND WHAT DELIBERATELY DOES NOT.
--
-- Here: the association between a Discord account and a Minecraft one, and the
-- short-lived requests that produce it. That is the only fact in this project
-- with no other home. Everything else already has one and is NOT copied in:
--
--   playtime, deaths, distance   world/stats/<uuid>.json, written by Minecraft
--   advancements                 world/advancements/<uuid>.json
--   who is online                RCON `list`
--   pack version and hash        pack.toml
--   spend                        /var/lib/wly/cost.json
--
-- Copying those into Postgres would create a second source that can disagree
-- with the first, and the first is authoritative. The database answers exactly
-- one question: whose Minecraft account is this?
--
-- BACKUPS, AND THE ONE FLAG THAT MAKES THEM RESTORABLE.
--
-- The dump MUST be taken with `pg_dump --clean --if-exists`. Rehearsed against
-- postgres:17-alpine on 2026-08-24, and a plain dump does not survive:
--
--   plain dump  -> restore onto a fresh volume -> ERROR: relation
--                  "link_requests" already exists, psql exits 3
--   --clean     -> restore onto the same database -> exit 0, no errors
--
-- The reason is this file. It runs from /docker-entrypoint-initdb.d, so a FRESH
-- volume always has these tables before a restore begins, and a dump that only
-- knows how to CREATE them collides with itself. --clean --if-exists makes the
-- dump self-sufficient: it drops first, so it restores over an initialised
-- database and into an empty one alike. Both were rehearsed.
--
-- What the rehearsal actually checked, because a restore that returns rows
-- without their constraints is worse than one that fails: after restoring,
-- inserting a second player row with an already-taken mc_uuid still fails with
-- players_mc_uuid_key. The uniqueness that makes one account belong to one
-- person survives the round trip.

CREATE TABLE IF NOT EXISTS players (
    discord_id   TEXT        PRIMARY KEY,

    -- One Minecraft account belongs to exactly one Discord account. Without
    -- this constraint two people can claim the same player, and every role,
    -- leaderboard row and playtime number derived from it becomes a guess.
    mc_uuid      UUID        NOT NULL UNIQUE,

    -- The name as it was when they linked. Names change; the uuid does not, so
    -- this is for display only and nothing may ever match on it.
    mc_name      TEXT        NOT NULL,

    linked_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- First time we saw them authenticate. Feeds the `day-one` role, and it is
    -- recorded once and never updated, because the whole point is that it is
    -- the earliest date and not the most recent one.
    first_seen   TIMESTAMPTZ
);

-- An open `/link` waiting for its join attempt.
--
-- The proof is the join itself: ONLINE_MODE is true, so Mojang authenticates
-- before the whitelist is consulted and the server logs the authenticated uuid
-- either way. Nothing is granted to collect that proof, which is why this table
-- holds no token and no privilege, only an intent with a deadline.
CREATE TABLE IF NOT EXISTS link_requests (
    mc_uuid    UUID        PRIMARY KEY,   -- one open request per account, ever
    discord_id TEXT        NOT NULL,
    mc_name    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

-- One open request per Discord user as well, so nobody can hold several
-- accounts in flight at once and keep whichever authenticates first.
CREATE UNIQUE INDEX IF NOT EXISTS link_requests_one_per_user
    ON link_requests (discord_id);

-- Expired rows are swept rather than left to accumulate, because the uniqueness
-- constraints above are what enforce "one at a time" and a stale row would
-- block a legitimate retry forever.
CREATE INDEX IF NOT EXISTS link_requests_expiry ON link_requests (expires_at);
