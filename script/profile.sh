#!/bin/bash
# Quick profiling script for qws

set -e

PROFILE_DIR="profiles"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
CPU_PROF="${PROFILE_DIR}/cpu_${TIMESTAMP}.prof"
MEM_PROF="${PROFILE_DIR}/mem_${TIMESTAMP}.prof"

# Create profiles directory
mkdir -p "$PROFILE_DIR"

echo "🔍 Starting QWS with profiling..."
echo "   CPU profile: $CPU_PROF"
echo "   Memory profile: $MEM_PROF"
echo ""

# Run qws with profiling
./qws --cpuprofile="$CPU_PROF" --memprofile="$MEM_PROF"

echo ""
echo "✅ Profiling complete!"
echo ""
echo "📊 Analyzing results..."
echo ""

# Analyze CPU profile
echo "=== CPU Profile (Top 10) ==="
go tool pprof -top -nodecount=10 "$CPU_PROF" 2>/dev/null | head -20
echo ""

# Analyze Memory profile
echo "=== Memory Profile (Top 10) ==="
go tool pprof -top -nodecount=10 "$MEM_PROF" 2>/dev/null | head -20
echo ""

echo "💡 For detailed analysis:"
echo "   go tool pprof -http=:8080 $CPU_PROF"
echo "   go tool pprof -http=:8081 $MEM_PROF"
echo ""
echo "📁 Profiles saved in: $PROFILE_DIR/"
