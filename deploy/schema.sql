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
-- BACKUPS. deploy/backup.sh archives the Minecraft world and nothing else, so
-- this database is currently NOT backed up. Losing it means everyone re-links,
-- which is annoying rather than fatal, but it should be a pg_dump beside the
-- world tar before anyone depends on it.

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
