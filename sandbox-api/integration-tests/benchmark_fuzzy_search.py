#!/usr/bin/env python3
"""
Fuzzy Search Performance Benchmark

Compares the performance of:
1. find command via process API
2. grep command via process API  
3. Fuzzy search API (fzf-powered)
"""

import json
import os
import subprocess
import sys
import time
from typing import Dict, List, Tuple

import requests

# Configuration
API_URL = os.getenv("API_BASE_URL", "http://localhost:8080")
TEST_DIR = "/Users/mjoffre/Documents/blaxel/sandbox/next.js"
QUERIES = ["app", "page", "component", "router", "server"]

# Colors
class Colors:
    RED = '\033[0;31m'
    GREEN = '\033[0;32m'
    YELLOW = '\033[1;33m'
    BLUE = '\033[0;34m'
    NC = '\033[0m'  # No Color


def print_header(text: str):
    """Print a colored header"""
    print(f"\n{Colors.BLUE}{'=' * 40}{Colors.NC}")
    print(f"{Colors.BLUE}{text}{Colors.NC}")
    print(f"{Colors.BLUE}{'=' * 40}{Colors.NC}\n")


def format_time(ms: int) -> str:
    """Format time in milliseconds to human-readable format"""
    if ms < 1000:
        return f"{ms}ms"
    else:
        return f"{ms/1000:.2f}s"


def run_find_via_api(query: str) -> Tuple[int, int]:
    """Run find command via process API and return (time_ms, result_count)"""
    start = time.time()
    
    # Execute find command
    cmd = f"find {TEST_DIR} -type f -iname \"*{query}*\""
    response = requests.post(
        f"{API_URL}/process",
        json={
            "command": cmd,
            "waitForCompletion": True,
            "timeout": 30,
            "workingDir": TEST_DIR,
        }
    )
    
    if response.status_code != 200:
        print(f"{Colors.RED}Failed to execute find: {response.text}{Colors.NC}")
        return int((time.time() - start) * 1000), 0
   
    elapsed_ms = int((time.time() - start) * 1000)
    result_count = response.json().get("resultCount", 0)
    return elapsed_ms, result_count


def run_grep_via_api(query: str) -> Tuple[int, int]:
    """Run grep command via process API and return (time_ms, result_count)"""
    start = time.time()
    
    # Execute grep command
    cmd = f"grep -r -l -i \"{query}\" \"{TEST_DIR}\""
    response = requests.post(
        f"{API_URL}/process",
        json={
            "command": cmd,
            "waitForCompletion": True,
            "timeout": 30
        }
    )
    
    if response.status_code != 200:
        print(f"{Colors.RED}Failed to execute grep: {response.text}{Colors.NC}")
        return int((time.time() - start) * 1000), 0
    
    elapsed_ms = int((time.time() - start) * 1000)
    result_count = response.json().get("resultCount", 0)
    return elapsed_ms, result_count


def run_fuzzy_search_api(query: str) -> Tuple[int, int]:
    """Run fuzzy search API and return (time_ms, result_count)"""
    start = time.time()
    
    params = {
        "query": query,
        "directory": TEST_DIR,
        "includeFiles": "true",
        "excludeDirs": "node_modules,.git",
        "maxResults": "100"
    }
    
    response = requests.get(f"{API_URL}/filesystem-search", params=params)
    
    if response.status_code != 200:
        print(f"{Colors.RED}Fuzzy search failed: {response.text}{Colors.NC}")
        return int((time.time() - start) * 1000), 0
    
    data = response.json()
    result_count = data.get("total", 0)
    
    elapsed_ms = int((time.time() - start) * 1000)
    return elapsed_ms, result_count


def main():
    print_header("Fuzzy Search Performance Benchmark")
    
    # Check if test directory exists
    if not os.path.exists(TEST_DIR):
        print(f"{Colors.RED}Error: Directory {TEST_DIR} does not exist{Colors.NC}")
        sys.exit(1)
    
    print(f"{Colors.GREEN}Using existing repository: next.js{Colors.NC}")
    print(f"  Location: {TEST_DIR}\n")
    
    # Count total files
    result = subprocess.run(
        ["find", TEST_DIR, "-type", "f"],
        capture_output=True,
        text=True
    )
    total_files = len([line for line in result.stdout.split('\n') if line.strip()])
    print(f"Repository stats: {total_files} files\n")
    
    print_header("Running Benchmarks")
    
    # Store results
    results = {
        "find": {"times": [], "counts": []},
        "grep": {"times": [], "counts": []},
        "fuzzy": {"times": [], "counts": []}
    }
    
    # Run benchmarks for each query
    for query in QUERIES:
        print(f"{Colors.YELLOW}Query: '{query}'{Colors.NC}")
        print("-" * 40)
        
        # 1. Find via process API
        print(f"  [1/3] Running find via process API... ", end="", flush=True)
        find_time, find_count = run_find_via_api(query)
        results["find"]["times"].append(find_time)
        results["find"]["counts"].append(find_count)
        print(f"{Colors.GREEN}✓{Colors.NC} {format_time(find_time)} ({find_count} results)")
        
        # 2. Grep via process API
        print(f"  [2/3] Running grep via process API... ", end="", flush=True)
        grep_time, grep_count = run_grep_via_api(query)
        results["grep"]["times"].append(grep_time)
        results["grep"]["counts"].append(grep_count)
        print(f"{Colors.GREEN}✓{Colors.NC} {format_time(grep_time)} ({grep_count} results)")
        
        # 3. Fuzzy search API
        print(f"  [3/3] Running fuzzy search API... ", end="", flush=True)
        fuzzy_time, fuzzy_count = run_fuzzy_search_api(query)
        results["fuzzy"]["times"].append(fuzzy_time)
        results["fuzzy"]["counts"].append(fuzzy_count)
        print(f"{Colors.GREEN}✓{Colors.NC} {format_time(fuzzy_time)} ({fuzzy_count} results)")
        
        # Calculate speedups
        if fuzzy_time > 0:
            find_speedup = find_time / fuzzy_time
            grep_speedup = grep_time / fuzzy_time
            print(f"  {Colors.BLUE}→{Colors.NC} Fuzzy API is {find_speedup:.2f}x vs find, {grep_speedup:.2f}x vs grep")
        
        # Show top fuzzy results
        params = {
            "query": query,
            "directory": TEST_DIR,
            "includeFiles": "true",
            "excludeDirs": "node_modules,.git",
            "maxResults": "3"
        }
        response = requests.get(f"{API_URL}/filesystem-search", params=params)
        if response.status_code == 200:
            data = response.json()
            matches = data.get("matches", [])
            if matches:
                print(f"  {Colors.BLUE}Top fuzzy matches:{Colors.NC}")
                for match in matches[:3]:
                    basename = os.path.basename(match["path"])
                    print(f"    [score: {match['score']}] {basename}")
        
        print()
    
    # Print summary table
    print_header("Performance Summary")
    
    print(f"{'Query':<10} | {'find':<15} | {'grep':<15} | {'fuzzy API':<15}")
    print(f"{'-'*10}-+-{'-'*15}-+-{'-'*15}-+-{'-'*15}")
    
    for i, query in enumerate(QUERIES):
        find_time = format_time(results["find"]["times"][i])
        grep_time = format_time(results["grep"]["times"][i])
        fuzzy_time = format_time(results["fuzzy"]["times"][i])
        print(f"{query:<10} | {find_time:<15} | {grep_time:<15} | {fuzzy_time:<15}")
    
    print()
    
    # Calculate averages
    avg_find = sum(results["find"]["times"]) / len(results["find"]["times"])
    avg_grep = sum(results["grep"]["times"]) / len(results["grep"]["times"])
    avg_fuzzy = sum(results["fuzzy"]["times"]) / len(results["fuzzy"]["times"])
    
    print(f"{Colors.YELLOW}Average Times:{Colors.NC}")
    print(f"  find:      {format_time(int(avg_find))}")
    print(f"  grep:      {format_time(int(avg_grep))}")
    print(f"  fuzzy API: {format_time(int(avg_fuzzy))}\n")
    
    if avg_fuzzy > 0:
        find_speedup = avg_find / avg_fuzzy
        grep_speedup = avg_grep / avg_fuzzy
        print(f"{Colors.GREEN}Average Speedup:{Colors.NC}")
        print(f"  Fuzzy API is {find_speedup:.2f}x faster than find")
        print(f"  Fuzzy API is {grep_speedup:.2f}x faster than grep")
    
    # Print advantages
    print_header("Key Advantages of Fuzzy Search API")
    advantages = [
        "Single HTTP request (no process spawning)",
        "No waiting for process completion/polling",
        "Built-in fzf algorithm for intelligent matching",
        "Scored results (best matches first)",
        "Configurable directory exclusions",
        "No shell escaping issues",
        "JSON response for easy parsing",
        "No need to parse stdout/stderr"
    ]
    
    for advantage in advantages:
        print(f"  {Colors.GREEN}✓{Colors.NC} {advantage}")
    
    print(f"\n{Colors.YELLOW}Note:{Colors.NC} find and grep times include process spawning + polling + log retrieval overhead\n")
    print(f"{Colors.GREEN}Benchmark completed!{Colors.NC}")


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        print(f"\n{Colors.YELLOW}Benchmark interrupted{Colors.NC}")
        sys.exit(0)
    except Exception as e:
        print(f"\n{Colors.RED}Error: {e}{Colors.NC}")
        sys.exit(1)

