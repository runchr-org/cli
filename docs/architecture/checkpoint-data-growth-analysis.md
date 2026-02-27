# Checkpoint Data Growth Analysis

Analysis of data volumes, push performance, and GitHub size limits as checkpoint usage scales across team sizes and customer counts.

## Methodology

- **Source data**: Real transcript analysis from the `entire/cli` repo (1140 checkpoints, 10 developers, ~2 months)
- **Push profiling**: Real GitHub push timing with `GIT_TRACE2_EVENT` phase breakdown
- **Compression ratio**: Measured from real transcripts packed with `git gc --aggressive` (11% pack/raw)
- **Model code**: `cmd/entire/cli/checkpoint/growth_model_test.go` (run with `-tags growthmodel`)

### Key Assumptions

| Parameter | Value | Source |
|---|---|---|
| Checkpoints/dev/day | 2.5 | 1100 checkpoints / 10 devs / 44 working days |
| Avg transcript size | 2.8 MB | Mean of 1140 real transcripts (range: 0.5 KB - 47.6 MB) |
| Git pack ratio | 11% | Profiled with real data at 2.5MB tier, 100-200 checkpoints |
| Working days/month | 22 | Standard |
| Push throughput | ~2 MB/s | Measured HTTPS pack transfer to GitHub |
| Push fixed cost | ~1.1s | Ref negotiation + remote processing + overhead |

## Per-Repo Data Growth

### Raw Data (cumulative)

| Team Size | 1 month | 3 months | 6 months | 12 months |
|---|---|---|---|---|
| 10 devs | 1.5 GB | 4.5 GB | 9.0 GB | 18.0 GB |
| 50 devs | 7.5 GB | 22.6 GB | 45.1 GB | 90.2 GB |
| 250 devs | 37.6 GB | 112.8 GB | 225.6 GB | 451.2 GB |
| 1000 devs | 150.4 GB | 451.2 GB | 902.3 GB | 1.8 TB |

### Git Pack Size (on disk / transferred)

| Team Size | 1 month | 3 months | 6 months | 12 months |
|---|---|---|---|---|
| 10 devs | 169 MB | 508 MB | 1016 MB | 2.0 GB |
| 50 devs | 847 MB | 2.5 GB | 5.0 GB | 9.9 GB |
| 250 devs | 4.1 GB | 12.4 GB | 24.8 GB | 49.6 GB |
| 1000 devs | 16.5 GB | 49.6 GB | 99.3 GB | 198.5 GB |

### Checkpoint Counts

| Team Size | 1 month | 3 months | 6 months | 12 months |
|---|---|---|---|---|
| 10 devs | 550 | 1,650 | 3,300 | 6,600 |
| 50 devs | 2,750 | 8,250 | 16,500 | 33,000 |
| 250 devs | 13,750 | 41,250 | 82,500 | 165,000 |
| 1000 devs | 55,000 | 165,000 | 330,000 | 660,000 |

## Push Time Projections

### First Push (full branch, all data new to remote)

| Team Size | 1 month | 3 months | 6 months | 12 months |
|---|---|---|---|---|
| 10 devs | 1.4 min | 4.3 min | 8.5 min | 17.0 min |
| 50 devs | 7.1 min | 21.2 min | 42.4 min | 1.4 hr |
| 250 devs | 35.3 min | 1.8 hr | 3.5 hr | 7.1 hr |
| 1000 devs | 2.4 hr | 7.1 hr | 14.1 hr | 28.2 hr |

**Incremental pushes (1 new checkpoint) are ~1-1.5s regardless of repo size.**

### Push Phase Breakdown (from profiling)

The push time is composed of three phases:

| Phase | Behavior | Typical Duration |
|---|---|---|
| **Ref negotiation** | Fixed cost (HTTPS handshake + ref listing) | ~500ms |
| **Pack + send** | Scales linearly with pack size at ~2 MB/s | Dominates for large repos |
| **Remote processing** | GitHub resolving deltas, updating refs | 400ms - 2s |

For small pushes (<1 MB pack), ref negotiation and remote processing dominate (~90% of push time). For large pushes (>10 MB pack), pack transfer dominates (80-92%).

## GitHub Size Limits

**Time to hit GitHub thresholds (git pack size on `entire/checkpoints/v1`):**

| Team Size | 1 GB (warning) | 5 GB (recommended max) | 10 GB (push issues) | 100 GB (hard limit) |
|---|---|---|---|---|
| 10 devs | 6.0 months | 30.2 months | 60.4 months | > 10 years |
| 50 devs | 1.2 months | 6.0 months | 12.1 months | > 10 years |
| 250 devs | **7 days** | 1.2 months | 2.4 months | 24.2 months |
| 1000 devs | **2 days** | **9 days** | **18 days** | 6.0 months |

## Platform-Level Storage

Assumes repos/company: small (10-dev) = 3 repos, medium (50-dev) = 8, large (250-dev) = 20, enterprise (1000-dev) = 60.

### Raw Data (total across all customers)

| Stage | 1 month | 3 months | 6 months | 12 months |
|---|---|---|---|---|
| Early (10 customers) | 903.8 GB | 2.6 TB | 5.3 TB | 10.6 TB |
| Growth (50 customers) | 24.5 TB | 73.5 TB | 146.9 TB | 293.9 TB |
| Scale (200 customers) | 162.3 TB | 487.0 TB | 974.1 TB | 1.9 PB |
| Mature (1000 customers) | 811.7 TB | 2.4 PB | 4.8 PB | 9.5 PB |

### Git Pack (11% of raw)

| Stage | 1 month | 3 months | 6 months | 12 months |
|---|---|---|---|---|
| Early (10 customers) | 99.4 GB | 298.3 GB | 596.5 GB | 1.2 TB |
| Growth (50 customers) | 2.7 TB | 8.1 TB | 16.2 TB | 32.3 TB |
| Scale (200 customers) | 17.9 TB | 53.6 TB | 107.1 TB | 214.3 TB |
| Mature (1000 customers) | 89.3 TB | 267.9 TB | 535.7 TB | 1.0 PB |

## Per-Developer Unit Economics

| Metric | Value |
|---|---|
| Checkpoints/dev/month | 55 |
| Raw data/dev/month | 154 MB |
| Git pack/dev/month | 16.9 MB |
| Raw data/dev/year | 1.8 GB |
| Git pack/dev/year | 203 MB |
| S3 cost (pack) | $0.0004/dev/month |
| S3 cost (raw) | $0.0035/dev/month |
| GitHub pack/dev/year | 203 MB |

Storage cost per developer is negligible. The constraint is GitHub's per-repo size limits and git's single-branch push architecture.

## Key Takeaways

1. **Small teams (10 devs) are fine for ~6 months** before hitting the 1 GB GitHub warning. The current architecture works well at this scale.

2. **Medium teams (50 devs) hit friction at ~6 months** when the 5 GB recommended limit is reached. Push times for first-push/clone scenarios become noticeable (20+ min).

3. **Large teams (250+ devs) need a data management strategy immediately.** They hit 1 GB in a week and 10 GB in 2.4 months.

4. **Incremental pushes stay fast** (~1-1.5s) regardless of repo size. The problem is first pushes, clones, and repo maintenance.

5. **Platform storage scales linearly** with no natural compression gains at scale. At 200 customers, we're at 50+ TB of git pack data after 3 months.

6. **The bottleneck is git, not storage cost.** S3 cost is <$0.01/dev/month. The real issues are:
   - GitHub repo size limits (warning at 1 GB, hard limit ~100 GB)
   - Push/clone times that grow linearly with accumulated data
   - No built-in data lifecycle management in git

## Potential Mitigations

- **Checkpoint pruning/retention policies** - Delete old checkpoint data after N days/months
- **zstd pre-compression** - Reduce transcript size before git stores it (currently being implemented)
- **Separate storage backend** - Move transcripts out of git to object storage (S3/GCS)
- **Shallow checkpoint branches** - Truncate `entire/checkpoints/v1` history periodically
- **Per-session deduplication** - Avoid storing duplicate transcripts across checkpoints

## Running the Model

```bash
# Run growth projections
go test -v -run TestGrowthModel -tags growthmodel ./cmd/entire/cli/checkpoint/

# Run push performance profiling (requires gh CLI + GitHub auth with delete_repo scope)
go test -v -run TestPushProfile -tags pushperf -timeout 30m ./cmd/entire/cli/checkpoint/

# Run full push performance test (all scenarios)
go test -v -run TestPushPerformance -tags pushperf -timeout 30m ./cmd/entire/cli/checkpoint/
```

Adjust constants at the top of `growth_model_test.go` to model different scenarios.
