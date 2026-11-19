# 0011 Performance method and the parallel model

Status: accepted (2026-11-19)

## Method

Benchmark, profile, optimize, guard: `internal/synth` generates a healthy fabric of any size (1000 switches: 10 regions of 2 spines and 98 leaves, about 600 table rows per switch), `make bench` runs fleet-scale benchmarks, `bench/baseline.txt` keeps the starting point. Every optimization is checked against the corpus for identical parse results.

## Decisions

- Snapshot loading, planning and checking run per switch on a bounded pool (`internal/parallel.Map`, GOMAXPROCS workers) and return results in input order, so output is identical to a sequential run. Rules are pure; per-fabric views (cabling audit) are computed once.
- Commands load only the dimensions they need (`LoadOptions.Dimensions`): planning, import and baselines read configurations (plus MAC tables when counting impact); receipts of other commands are not read.
- Hot table parsers (MAC table, l2route MAC and MAC-IP) tokenize into a reused buffer without regular expressions; receipts are cleaned by slicing; UTF-8 is validated once per receipt.

## Results (this machine, 1000 switches)

| | before | after |
|---|---|---|
| load all dimensions | 2.18 s, 1.98 GB | 0.35 s, 1.38 GB |
| load configurations | — | 0.14 s, 0.33 GB |
| check (34 → 42 rules) | 145 ms | 64 ms |
| plan | 54 ms | 21 ms |
| CLI `check` / `status` | — | 0.46 s / 0.48 s, about 1.2 GB RSS |

On the 83-switch corpus loading went from 188 ms to 35 ms with 54% fewer allocations and identical rows.
