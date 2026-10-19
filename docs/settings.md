# Bot settings (dashboard-managed)

The owner changes the bot's important switches and numbers from the dashboard's Settings tab, without a restart or a deploy. This page describes the bot side: the registry, the precedence, the tables, the settings goroutine, the change digest and the Telegram profile push. The dashboard only queues change requests; the bot validates and applies them within about 2 s. Changes that need the owner's Approve in Telegram (raising a budget, lowering the HikerAPI price, a free-service host) are rejected until that approval exists.

## Precedence

For every managed setting the bot uses, in this order:

1. the value the owner set (`bot_settings`), validated with the registry;
2. the `.env` variable, parsed exactly as before;
3. the code default.

With empty tables (the state after migration 011) every getter returns what it returned before, for any `.env`; `config/settings_test.go` proves it against copies of the previous getters (`config/head_getters_test.go`). `.env` stays the fallback: a key without a row, a reset, and a start whose settings read fails without a cache file all use it. A bot without a database configured (no `DB_*` variable set, and `InitDatabase` failed) leaves the settings inert: no snapshot, no cache file read or written, no alert, so every getter reads `.env` and its default exactly as before. A configured database that `InitDatabase` could not open at start (down, unreachable) is not "no database": the start falls back to the cache file like a failed settings read (below), so a platform the owner switched off stays off.

Exceptions:

- A stored value that no longer validates (for example after a range was narrowed in code) is skipped and logged once, so `.env` or the default applies. For money and payment keys it gives the key's safe value instead (budgets 0, HikerAPI price 1 USD, Stars off) and alerts the owner once, so a narrowed range never turns spending back on through `.env`.
- Hard ceilings apply to stored values only; `.env` values keep working as before.

## Registry (`model/settings`)

One entry per key: type, range, hard ceiling, `.env` variable, default, approval rule, safe value, and the label and impact text the owner sees. It is pure data plus stdlib validation (no I/O, nothing to change at runtime), like `model/reportreply`. The bot publishes it as `bot_runtime_status.registry`, so the dashboard needs no copy of its own.

| Group | Keys | `.env` |
|---|---|---|
| Platforms | `platform.<p>.enabled` for instagram, tiktok, twitter, youtube, reddit, facebook, 9gag, threads; `platform.instagram.fixed_link_service` | `ENABLE_<P>`, `INSTAGRAM_SERVICE` |
| Proxies & free services | `proxy.enabled`, `proxy.<p>.enabled` for youtube, twitter, tiktok, facebook, reddit, 9gag; `service.<name>.host` for kkinstagram, kktiktok, vxreddit, facebed | `ENABLE_PROXIES`, `ENABLE_<P>_PROXY`, `<NAME>_HOST` |
| Paid providers & budgets | `paid.apify.monthly_budget_usd`, `paid.hiker.monthly_budget_usd`, `paid.hiker.price_per_request_usd`, `paid.instagram.provider_order`, `alerts.hiker_low_balance_requests` | `APIFY_MONTHLY_BUDGET_USD`, `HIKERAPI_*`, `INSTAGRAM_PAID_PROVIDERS` |
| Restricted posts | `gated.global_monthly_cap`, `gated.user_monthly_allowance`, `gated.chat_monthly_cap` | `GATED_FETCH_*` |
| Stars and limits | `stars.enabled`, `limits.default_daily_media` | `STARS_SUPPORT_ENABLED`, `DEFAULT_DAILY_MEDIA_LIMIT` |

Hard ceilings (raising one needs a code change and a deploy): Apify budget 20 USD a month, HikerAPI budget 10 USD a month, bot-wide restricted posts 5000 a month, per person and per group 1000 a month.

Approval rules (data in the registry): raising a dollar budget (also from 0, which is how a provider is switched on), lowering the HikerAPI price, and any change of a `service.*.host` wait for the owner's Approve in Telegram. Everything else, Stars on included, applies directly. Until the Telegram approval exists, a request that needs it is rejected with "needs the owner's approval in Telegram, which is not available yet".

Never managed from the dashboard: tokens and other secrets, `ADMIN_USER_ID`, database and dashboard settings, Apify actor ids, payout links and addresses, the Stars tiers, the relay settings.

## Tables (migration `011_bot_settings`)

| Table | What |
|---|---|
| `bot_settings` | key, value (JSONB, at most 2 KB), `updated_at`, `updated_by`. Written by the bot when it applies a request; a trigger stamps `updated_by` from the writing transaction's `app.settings_actor` (`manual` when unset). `TRUNCATE` is refused |
| `settings_audit` | append-only history, one row per write of `bot_settings` (old and new value, actor, `db_role` = `current_user`, request id), filled by a trigger; `UPDATE`, `DELETE` and `TRUNCATE` are refused |
| `settings_change_requests` | what the dashboard asks for: it inserts `kind`, `key`, `value` (NULL = reset) and `expected` (the value the page showed) only. One open request per key (a unique index, so a second one fails at INSERT). A queued request expires 10 minutes after it was made |
| `bot_runtime_status` | what the bot reports, one JSON blob per component: `bot` (start, heartbeat, commit, where the settings came from), `secrets` (set or not, counts; never values), `settings` (effective value, source and whether `.env` sets it, per key), `registry`, `profile` (last profile push and its error) |

The down migration drops the four tables; a rollback is deploying the previous commit instead, which ignores them. The triggers need PostgreSQL 14 or later.

Grants for the dashboard role are an owner step on the server: `SELECT` on the four tables, `INSERT (kind, key, value, expected)` on `settings_change_requests` and `USAGE` on `settings_change_requests_id_seq`, nothing else (`services/botsettings/pg_integration_test.go` checks exactly these). They let the dashboard role change live settings once the bot applies requests, so they are run together with the Settings tab, after the dashboard's hardening (its own listener and reverse-proxy location for the settings API), not before.

## Settings goroutine (`services/botsettings`)

One goroutine owns the snapshot the getters read (`config/overrides`, lock-free), so a reload can never undo an apply:

- **Start**: loads `bot_settings` before the startup checks, so the first profile push already follows it. The read and the first publish each get at most 5 s (`startupTimeout`); a read that fails or takes longer counts as the database being unavailable. Then the bot uses the cache file `var/settings-cache.json` (git-ignored, written atomically after the start's load, every apply and every reload that changed the values; a write failure is logged once until a write succeeds again) and alerts the owner once: only the keys set in the dashboard come from the cache, every other key still comes from `.env` or its default. Without a cache file it uses `.env` and the defaults, also with one alert. When the connection was made but the read failed, the first successful reload (every 60 s) switches to the database values. When a configured database could not be opened at start (`InitDatabase` failed; `app/settings.go` passes its error as `Options.DatabaseErr`, no further database call), the bot uses the cache file or `.env` the same way with one alert, but runs no loop and publishes nothing: it does not reconnect, so the alert asks for a restart once the database is back. Without a database configured the settings stay inert (above) and a leftover cache file is ignored.
- **Every 2 s**, with `Options.ApplyRequests` on (`app/settings.go` turns it on; the heartbeat publishes it as `applies_requests`): claims the oldest queued request (`FOR UPDATE SKIP LOCKED`), checks the key, the value (type, range, ceiling), that the current value still equals `expected` ("changed meanwhile, reload") and the approval rule, then writes `bot_settings` and the request's outcome in one transaction. The snapshot changes only after the commit. While the bot runs on the cache file, requests wait for the first successful reload. With `Options.ApplyRequests` off (tests only), it only marks the queued requests past their `expires_at` `expired`, so a stale request never keeps its key's open-request slot.
- **Every 60 s**: re-reads `bot_settings` (picking up manual `psql` edits) and publishes the heartbeat, the secrets and the settings. `settings_loaded_at` in the heartbeat is the time of the last successful read, changed or not. A failed read keeps the current snapshot.
- **Change digest**: every applied change (from a request, and every key a reload finds changed outside the bot, e.g. by `psql`) goes into one plain-text Telegram message to the owner, at most one per 5 minutes: the first at the next 2 s tick, later ones once the 5 minutes have passed, the pending ones at shutdown. Each line is the bot's own rendering from the registry label and the old and new effective values, with the time (Kyiv) and the actor, e.g. "• TikTok off (21:40, dashboard)"; no text from the dashboard. It can not be muted, so a change nobody announced still reaches the owner. A failed send is retried in the next window; more than 20 changes end with "…and N more".
- **Profile**: after a change it re-renders the Telegram profile (command menu, description, short description) 10 s after the last change and pushes only the parts that differ from what Telegram shows, making up to 5 attempts with a growing wait, then alerting once. The platform lists of the description, the short description and `/start` / `/info` are built from the platform switches.

## Tests

```bash
go test ./model/settings ./config ./services/botsettings                    # registry, precedence matrix, goroutine (fake store)
SETTINGS_TEST_DSN=postgres://... go test ./services/botsettings             # plus migrations 001-011, 011 down/up, triggers, grants, races
```

The integration tests run the real migrations in their own schema and create a temporary role for the grants test, so point them at a throwaway database only.
