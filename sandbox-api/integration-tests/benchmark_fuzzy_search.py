#!/usr/bin/env python3
"""
Fuzzy Search & Content Search Performance Benchmark

Compares the performance of:
1. find command via process API (filename search)
2. grep command via process API (content search)
3. Fuzzy search API (fzf-powered filename search)
4. Content search API (ripgrep-powered content search)
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
        "includeFiles": "true",
        "excludeDirs": "node_modules,.git",
        "maxResults": "100"
    }
    
    # Use path-based endpoint
    encoded_path = TEST_DIR.replace("/", "%2F")
    response = requests.get(f"{API_URL}/filesystem-search/{encoded_path}", params=params)
    
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
        "fuzzy": {"times": [], "counts": []},
        "content": {"times": [], "counts": []}
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
        print(f"  [3/4] Running fuzzy search API... ", end="", flush=True)
        fuzzy_time, fuzzy_count = run_fuzzy_search_api(query)
        results["fuzzy"]["times"].append(fuzzy_time)
        results["fuzzy"]["counts"].append(fuzzy_count)
        print(f"{Colors.GREEN}✓{Colors.NC} {format_time(fuzzy_time)} ({fuzzy_count} results)")
        
        # 4. Content search API
        print(f"  [4/4] Running content search API... ", end="", flush=True)
        content_time, content_count = run_content_search_api(query)
        results["content"]["times"].append(content_time)
        results["content"]["counts"].append(content_count)
        print(f"{Colors.GREEN}✓{Colors.NC} {format_time(content_time)} ({content_count} results)")
        
        # Calculate speedups
        if fuzzy_time > 0:
            find_speedup = find_time / fuzzy_time
            print(f"  {Colors.BLUE}→{Colors.NC} Fuzzy API (fzf) is {find_speedup:.2f}x faster than find (process)")
        
        if content_time > 0:
            grep_speedup = grep_time / content_time
            print(f"  {Colors.BLUE}→{Colors.NC} Content API (rg) is {grep_speedup:.2f}x faster than grep (process)")
        
        # Show top fuzzy results
        params = {
            "query": query,
            "includeFiles": "true",
            "excludeDirs": "node_modules,.git",
            "maxResults": "3"
        }
        encoded_path = TEST_DIR.replace("/", "%2F")
        response = requests.get(f"{API_URL}/filesystem-search/{encoded_path}", params=params)
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
    
    print(f"{'Query':<10} | {'find':<12} | {'grep':<12} | {'fzf API':<12} | {'rg API':<12}")
    print(f"{'-'*10}-+-{'-'*12}-+-{'-'*12}-+-{'-'*12}-+-{'-'*12}")
    
    for i, query in enumerate(QUERIES):
        find_time = format_time(results["find"]["times"][i])
        grep_time = format_time(results["grep"]["times"][i])
        fuzzy_time = format_time(results["fuzzy"]["times"][i])
        content_time = format_time(results["content"]["times"][i])
        print(f"{query:<10} | {find_time:<12} | {grep_time:<12} | {fuzzy_time:<12} | {content_time:<12}")
    
    print()
    
    # Calculate averages
    avg_find = sum(results["find"]["times"]) / len(results["find"]["times"])
    avg_grep = sum(results["grep"]["times"]) / len(results["grep"]["times"])
    avg_fuzzy = sum(results["fuzzy"]["times"]) / len(results["fuzzy"]["times"])
    avg_content = sum(results["content"]["times"]) / len(results["content"]["times"])
    
    print(f"{Colors.YELLOW}Average Times:{Colors.NC}")
    print(f"  find (process):     {format_time(int(avg_find))}")
    print(f"  grep (process):     {format_time(int(avg_grep))}")
    print(f"  fzf API:            {format_time(int(avg_fuzzy))}")
    print(f"  ripgrep API:        {format_time(int(avg_content))}\n")
    
    print(f"{Colors.GREEN}Average Speedup:{Colors.NC}")
    if avg_fuzzy > 0:
        find_speedup = avg_find / avg_fuzzy
        print(f"  fzf API is {find_speedup:.2f}x faster than find (filename search)")
    
    if avg_content > 0:
        grep_speedup = avg_grep / avg_content
        print(f"  ripgrep API is {grep_speedup:.2f}x faster than grep (content search)")
    
    # Print advantages
    print_header("Key Advantages of New Search APIs")
    
    print(f"{Colors.YELLOW}fzf API (filename search):{Colors.NC}")
    fzf_advantages = [
        "Single HTTP request (no process spawning overhead)",
        "Uses find | fzf pipeline internally",
        "Intelligent fuzzy matching algorithm",
        "Scored results (best matches first)",
        "Configurable directory exclusions"
    ]
    for advantage in fzf_advantages:
        print(f"  {Colors.GREEN}✓{Colors.NC} {advantage}")
    
    print(f"\n{Colors.YELLOW}ripgrep API (content search):{Colors.NC}")
    rg_advantages = [
        "Single HTTP request (no process spawning overhead)",
        "10-100x faster than regular grep",
        "Returns line and column numbers",
        "JSON response with structured data",
        "Respects .gitignore patterns"
    ]
    for advantage in rg_advantages:
        print(f"  {Colors.GREEN}✓{Colors.NC} {advantage}")
    
    print(f"\n{Colors.YELLOW}Note:{Colors.NC} Process API times include spawning + waitForCompletion + log retrieval overhead\n")
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

