#!/usr/bin/env bash
# analyze-concurrency-heavy.sh — Analyze heavy concurrency probe results
#
# Reads results.csv from concurrency-probe-heavy.sh and outputs:
#   1. Per-level summary (count, success%, latency, tokens, throughput)
#   2. Token metrics per level (mean/total input, output, cache)
#   3. API duration analysis (mean/median/p95)
#   4. Throughput analysis (output tokens / wall clock time)
#   5. Utilization delta tracking
#   6. Rate limit status distribution
#   7. Cache hit ratio per level
#   8. Degradation breakpoint detection (extended criteria)
#
# Usage: ./scripts/analyze-concurrency-heavy.sh /tmp/concurrency-probe-heavy-*/results.csv
set -euo pipefail

CSV="${1:?Usage: $0 <results.csv>}"

if [ ! -f "$CSV" ]; then
    echo "ERROR: File not found: $CSV" >&2
    exit 1
fi

# Count data rows (skip header)
ROW_COUNT=$(tail -n +2 "$CSV" | wc -l | tr -d ' ')
if [ "$ROW_COUNT" -eq 0 ]; then
    echo "ERROR: No data rows in $CSV" >&2
    exit 1
fi

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║     HEAVY CONCURRENCY PROBE — Analysis Results              ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Input:  $CSV"
echo "║  Rows:   $ROW_COUNT"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# ── Full analysis using awk ──────────────────────────────────────────

# CSV columns (19):
# 1:level, 2:session, 3:spawn_ts, 4:first_output_ts, 5:complete_ts,
# 6:exit_code, 7:success, 8:failure_type, 9:startup_latency_ms, 10:total_latency_ms,
# 11:duration_ms, 12:duration_api_ms, 13:input_tokens, 14:output_tokens,
# 15:cache_read_tokens, 16:cache_creation_tokens, 17:utilization,
# 18:rate_limit_type, 19:rate_limit_status

tail -n +2 "$CSV" | awk -F',' '
BEGIN {
    level_idx = 0
}
{
    level = $1 + 0
    session = $2 + 0
    success = $7
    failure_type = $8
    startup_ms = $9 + 0
    total_ms = $10 + 0
    duration_ms = $11 + 0
    duration_api_ms = $12 + 0
    input_tokens = $13 + 0
    output_tokens = $14 + 0
    cache_read = $15 + 0
    cache_create = $16 + 0
    utilization = $17 + 0.0
    rl_type = $18
    rl_status = $19

    if (!(level in level_count)) {
        level_order[level_idx++] = level
        level_count[level] = 0
        level_success[level] = 0
        level_total_startup[level] = 0
        level_max_startup[level] = 0
        level_total_total[level] = 0
        level_total_duration_api[level] = 0
        level_total_input[level] = 0
        level_total_output[level] = 0
        level_total_cache_read[level] = 0
        level_total_cache_create[level] = 0
        level_min_spawn[level] = 9999999999999
        level_max_complete[level] = 0
        level_failures[level] = ""
        level_rl_statuses[level] = ""
        level_total_util[level] = 0
        level_util_count[level] = 0
    }

    level_count[level]++
    if (success == "true") level_success[level]++

    # Latency (use total if startup is 0)
    latency = startup_ms
    if (latency <= 0) latency = total_ms
    level_total_startup[level] += latency
    level_startup_vals[level, level_count[level]] = latency
    if (latency > level_max_startup[level]) level_max_startup[level] = latency

    level_total_total[level] += total_ms
    level_total_vals[level, level_count[level]] = total_ms

    # API duration
    level_total_duration_api[level] += duration_api_ms
    level_api_vals[level, level_count[level]] = duration_api_ms

    # Tokens
    level_total_input[level] += input_tokens
    level_total_output[level] += output_tokens
    level_total_cache_read[level] += cache_read
    level_total_cache_create[level] += cache_create

    # Wall clock bounds (for throughput)
    spawn_ts = $3 + 0
    complete_ts = $5 + 0
    if (spawn_ts < level_min_spawn[level]) level_min_spawn[level] = spawn_ts
    if (complete_ts > level_max_complete[level]) level_max_complete[level] = complete_ts

    # Utilization
    if (utilization > 0) {
        level_total_util[level] += utilization
        level_util_count[level]++
    }

    # Rate limit status
    if (rl_status != "" && rl_status != " ") {
        if (level_rl_statuses[level] != "")
            level_rl_statuses[level] = level_rl_statuses[level] "|" rl_status
        else
            level_rl_statuses[level] = rl_status
    }

    # Failures
    if (success != "true" && failure_type != "none") {
        if (level_failures[level] != "")
            level_failures[level] = level_failures[level] "," failure_type
        else
            level_failures[level] = failure_type
    }
}

function sort_arr(arr, n,    i, j, key) {
    for (j = 2; j <= n; j++) {
        key = arr[j]
        i = j - 1
        while (i > 0 && arr[i] > key) {
            arr[i + 1] = arr[i]
            i--
        }
        arr[i + 1] = key
    }
}

function median_val(arr, n) {
    if (n == 0) return 0
    if (n % 2 == 1) return int(arr[int(n/2) + 1])
    return int((arr[n/2] + arr[n/2 + 1]) / 2)
}

function p95_val(arr, n) {
    if (n == 0) return 0
    idx = int(n * 0.95)
    if (idx < 1) idx = 1
    if (idx > n) idx = n
    return int(arr[idx])
}

END {
    # ── 1. Per-Level Summary ──
    print "═══════════════════════════════════════════════════════════════"
    print " 1. Per-Level Summary"
    print "═══════════════════════════════════════════════════════════════"
    printf "%-7s %-5s %-7s %-10s %-10s %-10s %-10s %-10s\n", \
        "Level", "N", "Pass%", "Mean(ms)", "Med(ms)", "P95(ms)", "Max(ms)", "Failures"
    print "───────────────────────────────────────────────────────────────"

    # Store baseline values for breakpoint detection
    baseline_median = 0
    baseline_api_median = 0

    for (i = 0; i < level_idx; i++) {
        level = level_order[i]
        count = level_count[level]
        succ = level_success[level]
        succ_pct = (count > 0) ? int(succ * 100 / count) : 0
        mean = (count > 0) ? int(level_total_startup[level] / count) : 0

        # Sort startup latencies
        for (j = 1; j <= count; j++) sorted[j] = level_startup_vals[level, j]
        sort_arr(sorted, count)
        median = median_val(sorted, count)
        p95 = p95_val(sorted, count)
        max_val = int(level_max_startup[level])

        if (i == 0) baseline_median = median

        failures = level_failures[level]
        if (failures == "") failures = "-"

        printf "%-7s %-5s %-7s %-10s %-10s %-10s %-10s %-10s\n", \
            level, count, succ_pct "%", mean, median, p95, max_val, failures
    }

    # ── 2. Token Metrics ──
    print ""
    print "═══════════════════════════════════════════════════════════════"
    print " 2. Token Metrics Per Level"
    print "═══════════════════════════════════════════════════════════════"
    printf "%-7s %-10s %-10s %-10s %-12s %-12s %-10s\n", \
        "Level", "Mean In", "Mean Out", "Total In", "Total Out", "Cache Read", "Cache Hit%"
    print "───────────────────────────────────────────────────────────────"

    for (i = 0; i < level_idx; i++) {
        level = level_order[i]
        count = level_count[level]
        mean_in = (count > 0) ? int(level_total_input[level] / count) : 0
        mean_out = (count > 0) ? int(level_total_output[level] / count) : 0
        total_in = level_total_input[level]
        total_out = level_total_output[level]
        total_cache = level_total_cache_read[level]

        # Cache hit ratio: cache_read / (cache_read + input)
        total_all_input = total_in + total_cache
        cache_pct = (total_all_input > 0) ? int(total_cache * 100 / total_all_input) : 0

        printf "%-7s %-10s %-10s %-10s %-12s %-12s %-10s\n", \
            level, mean_in, mean_out, total_in, total_out, total_cache, cache_pct "%"
    }

    # ── 3. API Duration Analysis ──
    print ""
    print "═══════════════════════════════════════════════════════════════"
    print " 3. API Duration Analysis (duration_api_ms)"
    print "═══════════════════════════════════════════════════════════════"
    printf "%-7s %-10s %-10s %-10s %-10s\n", \
        "Level", "Mean(ms)", "Med(ms)", "P95(ms)", "Max(ms)"
    print "───────────────────────────────────────────────────────────────"

    for (i = 0; i < level_idx; i++) {
        level = level_order[i]
        count = level_count[level]
        mean_api = (count > 0) ? int(level_total_duration_api[level] / count) : 0

        for (j = 1; j <= count; j++) sorted[j] = level_api_vals[level, j]
        sort_arr(sorted, count)
        med_api = median_val(sorted, count)
        p95_api = p95_val(sorted, count)
        max_api = sorted[count]

        if (i == 0) baseline_api_median = med_api

        printf "%-7s %-10s %-10s %-10s %-10s\n", \
            level, mean_api, med_api, p95_api, int(max_api)
    }

    # ── 4. Throughput Analysis ──
    print ""
    print "═══════════════════════════════════════════════════════════════"
    print " 4. Throughput Analysis"
    print "═══════════════════════════════════════════════════════════════"
    printf "%-7s %-12s %-14s %-14s %-12s\n", \
        "Level", "Wall(ms)", "Total Out Tok", "Tok/sec", "Speedup"
    print "───────────────────────────────────────────────────────────────"

    baseline_tps = 0
    for (i = 0; i < level_idx; i++) {
        level = level_order[i]
        wall_ms = level_max_complete[level] - level_min_spawn[level]
        if (wall_ms <= 0) wall_ms = 1
        total_out = level_total_output[level]
        tps = total_out * 1000.0 / wall_ms

        if (i == 0) baseline_tps = tps
        speedup = (baseline_tps > 0) ? tps / baseline_tps : 1.0

        printf "%-7s %-12s %-14s %-14.1f %-12.2f\n", \
            level, int(wall_ms), total_out, tps, speedup
    }

    # ── 5. Utilization Tracking ──
    print ""
    print "═══════════════════════════════════════════════════════════════"
    print " 5. Utilization Tracking"
    print "═══════════════════════════════════════════════════════════════"
    printf "%-7s %-12s %-15s\n", "Level", "Mean Util", "Rate Limit Status"
    print "───────────────────────────────────────────────────────────────"

    for (i = 0; i < level_idx; i++) {
        level = level_order[i]
        mean_util = ""
        if (level_util_count[level] > 0) {
            mean_util = sprintf("%.4f", level_total_util[level] / level_util_count[level])
        } else {
            mean_util = "n/a"
        }

        rl = level_rl_statuses[level]
        if (rl == "") rl = "-"

        # Count unique statuses
        n = split(rl, parts, "|")
        delete counts
        for (j = 1; j <= n; j++) counts[parts[j]]++
        status_summary = ""
        for (s in counts) {
            if (status_summary != "") status_summary = status_summary ", "
            status_summary = status_summary s ":" counts[s]
        }
        if (status_summary == "") status_summary = "-"

        printf "%-7s %-12s %-15s\n", level, mean_util, status_summary
    }

    # ── 6. Degradation Analysis ──
    print ""
    print "═══════════════════════════════════════════════════════════════"
    print " 6. Degradation Analysis"
    print "═══════════════════════════════════════════════════════════════"

    breakpoint_level = 0
    breakpoint_reason = ""

    for (i = 0; i < level_idx; i++) {
        level = level_order[i]
        count = level_count[level]
        succ = level_success[level]
        succ_pct = (count > 0) ? int(succ * 100 / count) : 0

        # Sort startup latencies
        for (j = 1; j <= count; j++) sorted[j] = level_startup_vals[level, j]
        sort_arr(sorted, count)
        median = median_val(sorted, count)

        # Sort API durations
        for (j = 1; j <= count; j++) sorted[j] = level_api_vals[level, j]
        sort_arr(sorted, count)
        med_api = median_val(sorted, count)

        # Throughput
        wall_ms = level_max_complete[level] - level_min_spawn[level]
        if (wall_ms <= 0) wall_ms = 1
        tps = level_total_output[level] * 1000.0 / wall_ms

        # Mean utilization
        mean_util = 0
        if (level_util_count[level] > 0) {
            mean_util = level_total_util[level] / level_util_count[level]
        }

        if (breakpoint_level == 0) {
            # Check: success drop
            if (succ_pct < 100) {
                breakpoint_level = level
                breakpoint_reason = "success dropped to " succ_pct "%"
            }
            # Check: latency spike (2x baseline)
            else if (baseline_median > 0 && median > baseline_median * 2) {
                breakpoint_level = level
                breakpoint_reason = "median startup " median "ms > 2x baseline (" baseline_median "ms)"
            }
            # Check: API duration spike (2x baseline)
            else if (baseline_api_median > 0 && med_api > baseline_api_median * 2) {
                breakpoint_level = level
                breakpoint_reason = "median API duration " med_api "ms > 2x baseline (" baseline_api_median "ms)"
            }
            # Check: throughput collapse (< 50% of baseline)
            else if (baseline_tps > 0 && tps < baseline_tps * 0.5) {
                breakpoint_level = level
                breakpoint_reason = sprintf("throughput collapsed to %.1f tok/s (baseline: %.1f)", tps, baseline_tps)
            }
            # Check: utilization > 0.90
            else if (mean_util > 0.90) {
                breakpoint_level = level
                breakpoint_reason = sprintf("utilization %.2f > 0.90", mean_util)
            }
            # Check: rate limit denied
            else if (index(level_rl_statuses[level], "denied") > 0) {
                breakpoint_level = level
                breakpoint_reason = "rate_limit denied detected"
            }
        }
    }

    if (breakpoint_level > 0) {
        print "Breakpoint: Level " breakpoint_level " (" breakpoint_reason ")"
        print ""

        # Recommendation: one level below breakpoint
        rec = 0
        for (i = 0; i < level_idx; i++) {
            if (level_order[i] + 0 < breakpoint_level + 0)
                rec = level_order[i]
        }
        if (rec == 0) rec = 1

        print "═══════════════════════════════════════════════════════════════"
        print " Recommendation"
        print "═══════════════════════════════════════════════════════════════"
        print "Suggested --max-parallel: " rec
        print "Rationale: Highest level before degradation breakpoint"
    } else {
        print "No degradation detected across all tested levels."
        print ""
        highest = level_order[level_idx - 1]
        print "═══════════════════════════════════════════════════════════════"
        print " Recommendation"
        print "═══════════════════════════════════════════════════════════════"
        print "Suggested --max-parallel: " highest " (or higher — no ceiling found)"
        print "Rationale: All levels showed 100% success with no degradation signals"
    }

    # ── 7. Failure Type Distribution ──
    print ""
    print "═══════════════════════════════════════════════════════════════"
    print " 7. Failure Type Distribution"
    print "═══════════════════════════════════════════════════════════════"

    delete ftypes
    for (i = 0; i < level_idx; i++) {
        level = level_order[i]
        f = level_failures[level]
        if (f != "" && f != "-") {
            n = split(f, parts, ",")
            for (j = 1; j <= n; j++) ftypes[parts[j]]++
        }
    }

    any_failures = 0
    for (ft in ftypes) {
        printf "  %s: %d occurrence(s)\n", ft, ftypes[ft]
        any_failures = 1
    }
    if (!any_failures) print "  No failures observed."

    # ── 8. Token Budget Impact ──
    print ""
    print "═══════════════════════════════════════════════════════════════"
    print " 8. Token Budget Impact"
    print "═══════════════════════════════════════════════════════════════"

    grand_input = 0
    grand_output = 0
    grand_cache_read = 0
    grand_cache_create = 0
    for (i = 0; i < level_idx; i++) {
        level = level_order[i]
        grand_input += level_total_input[level]
        grand_output += level_total_output[level]
        grand_cache_read += level_total_cache_read[level]
        grand_cache_create += level_total_cache_create[level]
    }
    grand_total = grand_input + grand_output + grand_cache_read + grand_cache_create
    printf "  Total input tokens:          %d\n", grand_input
    printf "  Total output tokens:         %d\n", grand_output
    printf "  Total cache read tokens:     %d\n", grand_cache_read
    printf "  Total cache creation tokens: %d\n", grand_cache_create
    printf "  Grand total tokens:          %d\n", grand_total
}
'

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo " Raw Data"
echo "═══════════════════════════════════════════════════════════════"
cat "$CSV"
echo ""
