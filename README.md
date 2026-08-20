# pgav

PostgreSQL autovacuum advisor. A CLI that connects with a normal client, reads
the catalogs, and answers: **is vacuum keeping up with this table's writes, and
if not, what should change?**

No agent. No extension. `pgav tune` only prints SQL. Nothing is written
until you pass `--apply` (see below).

## The problem

Autovacuum is easy to ignore until it isn't. The defaults are built for small
tables. `autovacuum_vacuum_scale_factor = 0.2` on a 50 million row table means
vacuum does not even *start* until you have ~10 million dead tuples. A busy
`sessions` table can produce that in an afternoon. A leftover `idle in
transaction` session can freeze xmin so nothing gets reclaimed at all, no
matter how you set the GUCs.

What most tools show you is `n_dead_tup`. That number is a symptom. It does
not tell you whether vacuum is slow, late, or blocked. The usual fix is to
paste the same `scale_factor` / `cost_limit` onto every table from a blog
post. Quiet tables get vacuumed too often. Hot tables still lose. Then
someone runs a manual `VACUUM` at 2am and the graph goes green until next
week.

pgav models the race instead:

```
dead tuples per hour   vs   vacuum capacity per hour
         \                        /
          when does the trigger fire?
```

If the write rate is higher than reclaim rate, lowering `scale_factor` only
makes vacuum start sooner. It still cannot finish. You need more I/O budget
(`cost_limit`). If an idle transaction is holding xmin, you need to kill that
session first. The tool will say so.

## Commands

```
pgav doctor              cluster score and findings
pgav status              one line per table
pgav analyze TABLE       rate vs capacity for one table
pgav tune                print ALTER TABLE (nothing applied)
pgav tune --apply        execute that SQL
```

Connection is `--dsn`, `PGAV_DSN`, or the usual libpq variables (`PGHOST`,
`PGUSER`, `PGDATABASE`, …).

```
pgav doctor --dsn 'postgres://app@db:5432/app?sslmode=require'
```

## What it looks like

Example against the lab (throttled `sessions`, one leftover idle xact).
Absolute numbers move with how long the lab has been up:

```
Autovacuum Health: 72/100    2/4 tables OK

CRITICAL  public.sessions
  vacuum cannot keep up with the write workload
  dead 250K (69%)  rate 15.7M/h  est. reclaim ~9M/h  freeze 0%  keep up NO

HIGH  public.orders
  dead tuple ratio is high
  dead 200K (33%)  rate 12.6M/h  est. reclaim ~900M/h  freeze 0%  keep up yes

WARNING  cluster
  idle transaction is holding xmin and blocking vacuum on every table
  pid 92  app=pgav-lab-blocker  idle in transaction  49s
  SELECT pg_terminate_backend(92);

Next:
  Terminate idle transactions first. They block vacuum on every table.
  pgav analyze public.sessions
  pgav tune            # dry-run; nothing is applied
```

`events` staying OK is the point. The tool should classify, not panic at the
whole database.

`pgav analyze public.sessions` prints the race:

```
Race:
  dead tuples     250K (69% of table)
  write rate      15.7M dead tuples/hour
  est. reclaim    ~9M tuples/hour  (cost_limit/delay model, not IOPS)
  keeping up      NO
```

`pgav tune` then prints the change it would make, labeled as not executed:

```
DRY-RUN    2 table(s) would change    nothing applied

Blocked: idle transaction is holding xmin
  Settings can change, but dead tuples will not be reclaimed until this ends.
  SELECT pg_terminate_backend(92);

public.sessions
  cost_limit    2 -> 400
  I/O budget     ×200  (cost_limit 2 -> 400; estimate, not IOPS)
  why: vacuum cannot keep up with the write rate; raise cost_limit so reclaim can match writes

SQL (not executed)
ALTER TABLE "public"."sessions" SET (
    autovacuum_vacuum_cost_limit = 400
);

Next:
  SELECT pg_terminate_backend(92);  -- release xmin, then re-run
  pgav tune --apply    # execute the SQL above
```

## What `--apply` does

`pgav tune` is read-only. `--apply` is the only flag that mutates Postgres.

It takes the same `ALTER TABLE ... SET (...)` statements from the dry-run and
runs them in **one transaction**. If a statement fails, the whole batch rolls
back. Anything that is not `ALTER TABLE` is refused. It does not run `VACUUM`,
does not edit `postgresql.conf`, and does not restart the server.

The knobs it may set, per table:

| setting | what changes |
|---|---|
| `autovacuum_vacuum_scale_factor` | how much of the table must be dead before vacuum *starts* |
| `autovacuum_vacuum_threshold` | the fixed floor on that trigger |
| `autovacuum_vacuum_cost_limit` | how hard vacuum may hit the disk once it is running |

Those are storage parameters on the table. They survive reconnects and
restarts until you `ALTER` them again. They do not rewrite data.

Lowering the trigger (scale / threshold) makes vacuum start sooner: less
bloat between runs, more vacuum CPU/IO. Raising `cost_limit` is the usual
fix when `KEEP UP = NO`. Vacuum can reclaim faster, but that I/O comes off
the same disks as the application. On a large table at peak hours that
shows up as IO wait. The dry-run prints the before/after
(`cost_limit 2 -> 400`, I/O budget ×N) so you can see the size of the
change before you take it.

Settings take effect on the **next** autovacuum cycle. Dead tuples do not
drop at commit time. Wait at least one `autovacuum_naptime` (15s is a common
default; some places use 1 minute) and look at `pgav status` again.

**Do this before `--apply`:**

1. Read the dry-run. If the SQL is not what you meant, stop.
2. Kill `idle in transaction` sessions first. Doctor and tune both print
   `SELECT pg_terminate_backend(<pid>)`. If xmin is held, the `ALTER`
   still succeeds and **dead tuples will not fall**. That is Postgres
   working as designed.
3. You need permission to `ALTER` those tables (owner, or a role with that
   privilege). `doctor` / `status` / `analyze` / `tune` without `--apply`
   only need to read catalogs (`pg_stat_*`, `pg_class`, `pg_stat_activity`).
4. Run it on the lab, or staging, before production. Do not raise
   `cost_limit` on the busiest table at 14:00 on a Friday unless you have
   watched the dry-run and have headroom.

**After `--apply`:** the banner switches to `APPLIED` / `SQL (executed)`.
Re-run `pgav doctor`. If the score barely moved, you still have an xmin
blocker, not a settings problem. To undo, `ALTER TABLE ... RESET
(autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold,
autovacuum_vacuum_cost_limit)` or `SET` the old values from the dry-run
diff (`2 -> 400` means the previous limit was 2).

## Install

Go 1.25+ (the module's toolchain will download if needed).

```
git clone https://github.com/santiagolertora/pgav
cd pgav
task build          # or: go build -o dist/pgav ./cmd/pgav
./dist/pgav version
```

`task test` runs unit tests with the race detector. `task test:integration`
needs Docker.

## Try it on a throwaway cluster

The lab starts Postgres 16 and a traffic generator that keeps the race
visible on purpose:

| table | load | what you should see |
|---|---|---|
| `sessions` | heavy UPDATEs, autovacuum I/O throttled | `KEEP UP = NO` |
| `orders` | steady UPDATEs, default scale 0.2 | dead piles up, vacuum can still keep up |
| `events` | almost no writes | `OK` |
| `customers` | idle + one `idle in transaction` | xmin blocker named `pgav-lab-blocker` |

```
task lab:reset      # wait ~20s for seed, then ~30s of traffic
task lab:doctor
task lab:analyze    # public.sessions
task lab:tune       # dry-run
```

Postgres is on `localhost:5433`:

```
export PGAV_DSN='postgres://pgav:pgav@127.0.0.1:5433/pgav?sslmode=disable'
export PGAV_LONG_XACT_AFTER=10s

./dist/pgav doctor
./dist/pgav analyze public.sessions
./dist/pgav tune
```

`task lab:down` removes the containers and volumes.

How the reclaim estimate is computed: [docs/race-model.md](docs/race-model.md).

## Config

Flags and env vars cover the usual cases. For thresholds and tuner bounds,
use YAML (`--config`, see [docs/pgav.example.yaml](docs/pgav.example.yaml)):

| env | meaning |
|---|---|
| `PGAV_DSN` | connection string |
| `PGAV_LONG_XACT_AFTER` | how old an idle transaction must be to flag (default `1h`) |
| `PGAV_LOG_LEVEL` | `warn` by default so reports stay clean |

Logs go to stderr. The report is stdout. In a pipeline you only see the
report.

## Status

Early. Tests cover the catalog queries and the race model. Run the lab
before you point this at a database you care about.

GNU Affero General Public License v3.0. See [LICENSE](LICENSE).
