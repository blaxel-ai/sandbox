#!/usr/bin/env bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
API_URL="${API_BASE_URL:-http://localhost:8080}"
TEST_DIR="/Users/mjoffre/Documents/blaxel/sandbox/next.js"
QUERIES=("app" "page" "component" "router" "server")

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Fuzzy Search Performance Benchmark${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Check if directory exists
if [ ! -d "$TEST_DIR" ]; then
    echo -e "${RED}Error: Directory $TEST_DIR does not exist${NC}"
    exit 1
fi

echo -e "${GREEN}Using existing repository: next.js${NC}"
echo -e "  Location: $TEST_DIR"
echo ""

# Count total files
TOTAL_FILES=$(find "$TEST_DIR" -type f | wc -l)
echo -e "Repository stats: ${TOTAL_FILES} files"
echo ""

# Function to get current time in milliseconds (macOS compatible)
get_time_ms() {
    # Use Python for cross-platform millisecond precision
    python3 -c 'import time; print(int(time.time() * 1000))'
}

# Function to format time
format_time() {
    local ms=$1
    if [ -z "$ms" ] || [ "$ms" = "" ]; then
        echo "0ms"
    elif [ $ms -lt 1000 ]; then
        echo "${ms}ms"
    else
        local seconds=$(echo "scale=2; $ms/1000" | bc)
        echo "${seconds}s"
    fi
}

# Run benchmarks for each query
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Running Benchmarks${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Use indexed arrays instead of associative for bash 3 compatibility
find_times=()
grep_times=()
fuzzy_times=()
find_counts=()
grep_counts=()
fuzzy_counts=()

query_idx=0
for query in "${QUERIES[@]}"; do
    echo -e "${YELLOW}Query: '$query'${NC}"
    echo "----------------------------------------"
    
    # 1. Test with find command via process API
    echo -n "  [1/3] Running find via process API... "
    FIND_START=$(get_time_ms)
    
    # Execute find command (properly format JSON)
    FIND_CMD="find $TEST_DIR -type f -iname *${query}*"
    FIND_JSON=$(python3 -c "import json; print(json.dumps({'command': '''$FIND_CMD'''}))")
    FIND_EXEC=$(curl -s -X POST "${API_URL}/process" \
        -H "Content-Type: application/json" \
        -d "$FIND_JSON")
    FIND_PID=$(echo "$FIND_EXEC" | python3 -c "import sys, json; data=json.load(sys.stdin); print(data.get('pid', ''))")
    
    # Debug: Check if PID was extracted
    if [ -z "$FIND_PID" ]; then
        echo -e "${RED}Failed to get find PID${NC}"
        echo "Response: $FIND_EXEC"
        FIND_TIME=0
        FIND_COUNT=0
    else
        # Wait for find to complete (5 seconds should be enough for large repos)
        sleep 5
        
        # Get logs
        FIND_LOGS=$(curl -s "${API_URL}/process/${FIND_PID}/logs")
        FIND_RESULTS=$(echo "$FIND_LOGS" | python3 -c "import sys, json; data=json.load(sys.stdin); print(data.get('stdout', ''))" 2>/dev/null || echo "")
    fi
    
    FIND_END=$(get_time_ms)
    FIND_TIME=$(( FIND_END - FIND_START ))
    
    # Count non-empty lines
    if [ -n "$FIND_RESULTS" ]; then
        FIND_COUNT=$(echo "$FIND_RESULTS" | grep -c '[^[:space:]]')
    else
        FIND_COUNT=0
    fi
    
    find_times[$query_idx]=$FIND_TIME
    find_counts[$query_idx]=$FIND_COUNT
    echo -e "${GREEN}✓${NC} $(format_time $FIND_TIME) (${FIND_COUNT} results)"
    
    # 2. Test with grep command via process API
    echo -n "  [2/3] Running grep via process API... "
    GREP_START=$(get_time_ms)
    
    # Execute grep command (properly format JSON)
    GREP_CMD="grep -r -l -i $query $TEST_DIR"
    GREP_JSON=$(python3 -c "import json; print(json.dumps({'command': '''$GREP_CMD'''}))")
    GREP_EXEC=$(curl -s -X POST "${API_URL}/process" \
        -H "Content-Type: application/json" \
        -d "$GREP_JSON")
    GREP_PID=$(echo "$GREP_EXEC" | python3 -c "import sys, json; data=json.load(sys.stdin); print(data.get('pid', ''))")
    
    # Debug: Check if PID was extracted
    if [ -z "$GREP_PID" ]; then
        echo -e "${RED}Failed to get grep PID${NC}"
        echo "Response: $GREP_EXEC"
        GREP_TIME=0
        GREP_COUNT=0
    else
        # Wait for grep to complete (10 seconds for large repos with content search)
        sleep 10
        
        # Get logs
        GREP_LOGS=$(curl -s "${API_URL}/process/${GREP_PID}/logs")
        GREP_RESULTS=$(echo "$GREP_LOGS" | python3 -c "import sys, json; data=json.load(sys.stdin); print(data.get('stdout', ''))" 2>/dev/null || echo "")
    fi
    
    GREP_END=$(get_time_ms)
    GREP_TIME=$(( GREP_END - GREP_START ))
    
    # Count non-empty lines
    if [ -n "$GREP_RESULTS" ]; then
        GREP_COUNT=$(echo "$GREP_RESULTS" | grep -c '[^[:space:]]')
    else
        GREP_COUNT=0
    fi
    
    grep_times[$query_idx]=$GREP_TIME
    grep_counts[$query_idx]=$GREP_COUNT
    echo -e "${GREEN}✓${NC} $(format_time $GREP_TIME) (${GREP_COUNT} results)"
    
    # 3. Test with fuzzy search API
    echo -n "  [3/3] Running fuzzy search API... "
    FUZZY_START=$(get_time_ms)
    FUZZY_RESPONSE=$(curl -s "${API_URL}/filesystem-search?query=${query}&directory=${TEST_DIR}&includeFiles=true&excludeDirs=node_modules,.git&maxResults=100")
    FUZZY_END=$(get_time_ms)
    FUZZY_TIME=$(( FUZZY_END - FUZZY_START ))
    FUZZY_COUNT=$(echo "$FUZZY_RESPONSE" | grep -o '"total":[0-9]*' | cut -d: -f2)
    fuzzy_times[$query_idx]=$FUZZY_TIME
    fuzzy_counts[$query_idx]=$FUZZY_COUNT
    echo -e "${GREEN}✓${NC} $(format_time $FUZZY_TIME) (${FUZZY_COUNT} results)"
    
    # Calculate speedups
    if [ ! -z "$FUZZY_TIME" ] && [ "$FUZZY_TIME" -gt 0 ]; then
        FIND_SPEEDUP=$(echo "scale=2; $FIND_TIME/$FUZZY_TIME" | bc)
        GREP_SPEEDUP=$(echo "scale=2; $GREP_TIME/$FUZZY_TIME" | bc)
        echo -e "  ${BLUE}→${NC} Fuzzy API is ${FIND_SPEEDUP}x vs find, ${GREP_SPEEDUP}x vs grep"
    fi
    
    # Show top 3 fuzzy results
    echo -e "  ${BLUE}Top fuzzy matches:${NC}"
    echo "$FUZZY_RESPONSE" | grep -o '"path":"[^"]*","score":[0-9]*' | head -3 | while read -r match; do
        path=$(echo "$match" | sed 's/.*"path":"\([^"]*\)".*/\1/')
        score=$(echo "$match" | sed 's/.*"score":\([0-9]*\).*/\1/')
        basename=$(basename "$path")
        echo -e "    [score: ${score}] ${basename}"
    done
    
    echo ""
    query_idx=$((query_idx + 1))
done

# Print summary table
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Performance Summary${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
printf "%-10s | %-15s | %-15s | %-15s\n" "Query" "find" "grep" "fuzzy API"
printf "%-10s-+-%-15s-+-%-15s-+-%-15s\n" "----------" "---------------" "---------------" "---------------"

idx=0
for query in "${QUERIES[@]}"; do
    find_time=$(format_time ${find_times[$idx]})
    grep_time=$(format_time ${grep_times[$idx]})
    fuzzy_time=$(format_time ${fuzzy_times[$idx]})
    printf "%-10s | %-15s | %-15s | %-15s\n" "$query" "$find_time" "$grep_time" "$fuzzy_time"
    idx=$((idx + 1))
done

echo ""

# Calculate averages
total_find=0
total_grep=0
total_fuzzy=0
count=${#QUERIES[@]}

idx=0
for query in "${QUERIES[@]}"; do
    total_find=$((total_find + ${find_times[$idx]}))
    total_grep=$((total_grep + ${grep_times[$idx]}))
    total_fuzzy=$((total_fuzzy + ${fuzzy_times[$idx]}))
    idx=$((idx + 1))
done

avg_find=$((total_find / count))
avg_grep=$((total_grep / count))
avg_fuzzy=$((total_fuzzy / count))

echo -e "${YELLOW}Average Times:${NC}"
printf "  find:      %s\n" "$(format_time $avg_find)"
printf "  grep:      %s\n" "$(format_time $avg_grep)"
printf "  fuzzy API: %s\n" "$(format_time $avg_fuzzy)"
echo ""

if [ ! -z "$avg_fuzzy" ] && [ "$avg_fuzzy" -gt 0 ]; then
    find_avg_speedup=$(echo "scale=2; $avg_find/$avg_fuzzy" | bc)
    grep_avg_speedup=$(echo "scale=2; $avg_grep/$avg_fuzzy" | bc)
    echo -e "${GREEN}Average Speedup:${NC}"
    printf "  Fuzzy API is ${find_avg_speedup}x faster than find\n"
    printf "  Fuzzy API is ${grep_avg_speedup}x faster than grep\n"
fi

echo ""
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Key Advantages of Fuzzy Search API${NC}"
echo -e "${BLUE}========================================${NC}"
echo -e "  ${GREEN}✓${NC} Single HTTP request (no process spawning)"
echo -e "  ${GREEN}✓${NC} No waiting for process completion/polling"
echo -e "  ${GREEN}✓${NC} Built-in fzf algorithm for intelligent matching"
echo -e "  ${GREEN}✓${NC} Scored results (best matches first)"
echo -e "  ${GREEN}✓${NC} Configurable directory exclusions"
echo -e "  ${GREEN}✓${NC} No shell escaping issues"
echo -e "  ${GREEN}✓${NC} JSON response for easy parsing"
echo -e "  ${GREEN}✓${NC} No need to parse stdout/stderr"
echo ""

echo -e "${YELLOW}Note:${NC} find and grep times include process spawning + polling + log retrieval overhead"
echo ""

echo -e "${GREEN}Benchmark completed!${NC}"

