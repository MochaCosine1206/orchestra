#!/bin/bash
# analyze-hierarchical.sh — Analyze hierarchical experiment CSV results
# Usage: ./scripts/analyze-hierarchical.sh <RESULTS_CSV>
set -uo pipefail

CSV="${1:?Usage: analyze-hierarchical.sh <RESULTS_CSV>}"

if [ ! -f "$CSV" ]; then
    echo "Error: $CSV not found"
    exit 1
fi

echo ""
echo "========================================="
echo "  Hierarchical Experiment Analysis"
echo "  Source: $CSV"
echo "========================================="
echo ""

# Per-arm summary
for arm in A B; do
    arm_label=$([ "$arm" = "A" ] && echo "Flat + FIFO" || echo "Hierarchical + FIFO")
    runs=$(awk -F, -v arm="$arm" 'NR>1 && $2==arm {count++} END {print count+0}' "$CSV")
    passes=$(awk -F, -v arm="$arm" 'NR>1 && $2==arm && $4==1 {count++} END {print count+0}' "$CSV")
    total_conflicts=$(awk -F, -v arm="$arm" 'NR>1 && $2==arm {sum+=$5} END {print sum+0}' "$CSV")
    total_auto=$(awk -F, -v arm="$arm" 'NR>1 && $2==arm {sum+=$6} END {print sum+0}' "$CSV")
    avg_wall=$(awk -F, -v arm="$arm" 'NR>1 && $2==arm {sum+=$7; count++} END {if(count>0) printf "%.0f", sum/count; else print "0"}' "$CSV")
    avg_tasks=$(awk -F, -v arm="$arm" 'NR>1 && $2==arm {sum+=$8; count++} END {if(count>0) printf "%.1f", sum/count; else print "0"}' "$CSV")
    avg_clusters=$(awk -F, -v arm="$arm" 'NR>1 && $2==arm {sum+=$9; count++} END {if(count>0) printf "%.1f", sum/count; else print "0"}' "$CSV")

    if [ "$runs" -gt 0 ]; then
        pass_rate=$(awk "BEGIN {printf \"%.0f\", ($passes/$runs)*100}")
    else
        pass_rate="N/A"
    fi

    echo "  Arm $arm: $arm_label"
    echo "    Runs:             $runs"
    echo "    Pass rate:        $passes/$runs ($pass_rate%)"
    echo "    Merge conflicts:  $total_conflicts total"
    echo "    Auto-resolved:    $total_auto total"
    echo "    Avg wall clock:   ${avg_wall}s"
    echo "    Avg tasks:        $avg_tasks"
    echo "    Avg clusters:     $avg_clusters"
    echo ""
done

# Tier breakdown
echo "  Per-tier breakdown:"
echo "  tier | arm | runs | pass_rate | avg_wall_s"
echo "  -----|-----|------|-----------|----------"
for tier in $(awk -F, 'NR>1 {print $3}' "$CSV" | sort -n | uniq); do
    for arm in A B; do
        runs=$(awk -F, -v arm="$arm" -v tier="$tier" 'NR>1 && $2==arm && $3==tier {count++} END {print count+0}' "$CSV")
        if [ "$runs" -gt 0 ]; then
            passes=$(awk -F, -v arm="$arm" -v tier="$tier" 'NR>1 && $2==arm && $3==tier && $4==1 {count++} END {print count+0}' "$CSV")
            avg_wall=$(awk -F, -v arm="$arm" -v tier="$tier" 'NR>1 && $2==arm && $3==tier {sum+=$7; count++} END {if(count>0) printf "%.0f", sum/count; else print "0"}' "$CSV")
            pass_rate=$(awk "BEGIN {printf \"%.0f\", ($passes/$runs)*100}")
            echo "  $tier    | $arm   | $runs    | ${pass_rate}%      | $avg_wall"
        fi
    done
done

echo ""
echo "  Raw data:"
cat "$CSV"
echo ""
