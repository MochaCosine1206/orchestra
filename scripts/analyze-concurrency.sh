#!/usr/bin/env bash
# analyze-concurrency.sh — Analyze concurrency probe results
#
# Reads results.csv from concurrency-probe.sh and outputs:
#   1. Per-level summary (count, success%, mean/median/p95 startup latency)
#   2. Degradation breakpoint (first level with >5s latency or <100% success)
#   3. Recommendation for --max-parallel default
#
# Usage: ./scripts/analyze-concurrency.sh /tmp/concurrency-probe-*/results.csv
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
echo "║         CONCURRENCY PROBE — Analysis Results                ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Input:  $CSV"
echo "║  Rows:   $ROW_COUNT"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# ── Per-level analysis using awk ─────────────────────────────────────

# CSV columns:
# 1:level, 2:session, 3:spawn_ts, 4:first_output_ts, 5:complete_ts,
# 6:exit_code, 7:success, 8:failure_type, 9:startup_latency_ms, 10:total_latency_ms

echo "═══════════════════════════════════════════════════════════════"
echo " Per-Level Summary"
echo "═══════════════════════════════════════════════════════════════"
printf "%-7s %-7s %-9s %-12s %-12s %-12s %-12s %-12s\n" \
    "Level" "Count" "Success%" "Mean(ms)" "Median(ms)" "P95(ms)" "Max(ms)" "Failures"
echo "───────────────────────────────────────────────────────────────"

# Use awk for the heavy lifting
tail -n +2 "$CSV" | awk -F',' '
BEGIN {
    # Track levels in order
    level_idx = 0
}
{
    level = $1
    success = $7
    failure_type = $8
    startup_ms = $9 + 0  # force numeric
    total_ms = $10 + 0

    if (!(level in level_count)) {
        level_order[level_idx++] = level
        level_count[level] = 0
        level_success[level] = 0
        level_total_startup[level] = 0
        level_max_startup[level] = 0
        level_failures[level] = ""
    }

    level_count[level]++
    if (success == "true") level_success[level]++

    # Use total_latency if startup is 0 (fallback)
    latency = startup_ms
    if (latency <= 0) latency = total_ms

    level_total_startup[level] += latency

    # Track all latencies for median/p95
    level_latencies[level, level_count[level]] = latency

    if (latency > level_max_startup[level])
        level_max_startup[level] = latency

    if (success != "true" && failure_type != "none") {
        if (level_failures[level] != "")
            level_failures[level] = level_failures[level] "," failure_type
        else
            level_failures[level] = failure_type
    }
}
END {
    breakpoint_level = 0
    breakpoint_reason = ""

    for (i = 0; i < level_idx; i++) {
        level = level_order[i]
        count = level_count[level]
        succ = level_success[level]
        succ_pct = (count > 0) ? int(succ * 100 / count) : 0
        mean = (count > 0) ? int(level_total_startup[level] / count) : 0
        max_val = int(level_max_startup[level])

        # Sort latencies for median and p95
        # Simple insertion sort (small N)
        for (j = 1; j <= count; j++) {
            sorted[j] = level_latencies[level, j]
        }
        for (j = 2; j <= count; j++) {
            key = sorted[j]
            k = j - 1
            while (k > 0 && sorted[k] > key) {
                sorted[k + 1] = sorted[k]
                k--
            }
            sorted[k + 1] = key
        }

        # Median
        if (count % 2 == 1)
            median = int(sorted[int(count / 2) + 1])
        else
            median = int((sorted[count / 2] + sorted[count / 2 + 1]) / 2)

        # P95
        p95_idx = int(count * 0.95)
        if (p95_idx < 1) p95_idx = 1
        if (p95_idx > count) p95_idx = count
        p95 = int(sorted[p95_idx])

        failures = level_failures[level]
        if (failures == "") failures = "-"

        printf "%-7s %-7s %-9s %-12s %-12s %-12s %-12s %-12s\n", \
            level, count, succ_pct "%", mean, median, p95, max_val, failures

        # Detect breakpoint: first level with >5000ms median startup or <100% success
        if (breakpoint_level == 0) {
            if (succ_pct < 100) {
                breakpoint_level = level
                breakpoint_reason = "success dropped to " succ_pct "%"
            } else if (median > 5000) {
                breakpoint_level = level
                breakpoint_reason = "median startup " median "ms > 5000ms"
            }
        }
    }

    print ""
    print "═══════════════════════════════════════════════════════════════"
    print " Degradation Analysis"
    print "═══════════════════════════════════════════════════════════════"

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
        print "Rationale: Highest level with 100% success and <5s median startup"
        print ""
        print "Note: This was a snapshot test. Limits may vary by time of day,"
        print "account utilization, and server-side changes."
    } else {
        print "No degradation detected across all tested levels."
        print ""
        # Find highest tested level
        highest = level_order[level_idx - 1]
        print "═══════════════════════════════════════════════════════════════"
        print " Recommendation"
        print "═══════════════════════════════════════════════════════════════"
        print "Suggested --max-parallel: " highest " (or higher — no ceiling found)"
        print "Rationale: All levels showed 100% success with <5s median startup"
        print ""
        print "Consider testing higher levels (12, 16, 20) to find the actual ceiling."
    }

    # Output failure type distribution
    print ""
    print "═══════════════════════════════════════════════════════════════"
    print " Failure Type Distribution"
    print "═══════════════════════════════════════════════════════════════"

    # Count failure types across all levels
    split("", ftypes)
    for (i = 0; i < level_idx; i++) {
        level = level_order[i]
        f = level_failures[level]
        if (f != "" && f != "-") {
            n = split(f, parts, ",")
            for (j = 1; j <= n; j++) {
                ftypes[parts[j]]++
            }
        }
    }

    any_failures = 0
    for (ft in ftypes) {
        printf "  %s: %d occurrence(s)\n", ft, ftypes[ft]
        any_failures = 1
    }
    if (!any_failures) {
        print "  No failures observed."
    }
}
'

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo " Raw Data"
echo "═══════════════════════════════════════════════════════════════"
cat "$CSV"
echo ""
