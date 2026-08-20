# Autovacuum race model

pgav does not guess from `n_dead_tup` alone. It compares how fast a table
produces dead tuples with how fast autovacuum can reclaim them.

```
dead_rate  = (n_tup_upd - n_tup_hot_upd + n_tup_del) / stats_window_hours
trigger    = vacuum_threshold + vacuum_scale_factor * reltuples
est_reclaim = f(cost_limit, cost_delay, assumed cost/page)
lag         = dead_rate - est_reclaim
```

If `lag > 0` for a sustained window, bloat and wraparound are symptoms:
vacuum is losing the race.

`est_reclaim` is a **cost-throttling model**, not a measurement of IOPS. It
exists to rank tables and to size `cost_limit` against the write rate. Do not
quote it as disk capacity. The CLI labels it as an estimate.

`cost_delay` is read from table reloptions (PostgreSQL stores it as
milliseconds). Using the cluster default instead would invent numbers like
billions of tuples/hour.

`pgav tune` raises `cost_limit` to the smallest round step that covers
`dead_rate` with headroom. It does not bump `vacuum_threshold` just because
the default (50) is below an arbitrary floor. For very large tables it may
switch to `scale_factor = 0` plus an absolute threshold.

`doctor` / `status` / `analyze` show freeze age
(`age(relfrozenxid) / autovacuum_freeze_max_age`) and rows from
`pg_stat_progress_vacuum`. Wraparound outranks “cannot keep up”: an
impending freeze stall is the outage, not the bloat graph.
