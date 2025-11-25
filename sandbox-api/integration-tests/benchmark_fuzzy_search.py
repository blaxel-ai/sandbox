#!/usr/bin/env python3
"""
Simple benchmark: find (process API) vs HandleFind API

Compares:
1. find command via process API (spawns process, waits, parses logs)
2. HandleFind API (direct filesystem handler)
"""

import os
import sys
import time
from typing import Tuple

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
    NC = '\033[0m'


def format_time(ms: int) -> str:
    """Format time in milliseconds"""
    if ms < 1000:
        return f"{ms}ms"
    else:
        return f"{ms/1000:.2f}s"


def run_find_via_process(pattern: str) -> Tuple[int, int]:
    """Run find command via process API"""
    start = time.time()
    
    cmd = f"find {TEST_DIR} -type f -iname \"*{pattern}*\""
    response = requests.post(
        f"{API_URL}/process",
        json={
            "command": cmd,
            "waitForCompletion": True,
            "timeout": 30
        }
    )
    elapsed_ms = int((time.time() - start) * 1000)
    if response.status_code != 200:
        return int((time.time() - start) * 1000), 0
    
    pid = response.json().get("pid")
    
    # Get logs
    logs_response = requests.get(f"{API_URL}/process/{pid}/logs")
    if logs_response.status_code == 200:
        stdout = logs_response.json().get("stdout", "")
        result_count = len([line for line in stdout.split('\n') if line.strip()])
    else:
        result_count = 0
    
   
    return elapsed_ms, result_count


def run_handle_find(pattern: str) -> Tuple[int, int]:
    """Run HandleFind API directly"""
    start = time.time()
    
    # Encode path: /Users/... -> %2FUsers%2F...
    encoded_path = TEST_DIR.replace("/", "%2F")
    
    response = requests.get(
        f"{API_URL}/filesystem-find/{encoded_path}",
        params={
            "patterns": f"*{pattern}*",
            "excludeHidden": "true",
            "type": "file",
            "maxResults": "1000",
        }
    )
    elapsed_ms = int((time.time() - start) * 1000)
    if response.status_code != 200:
        print(f"{Colors.RED}HandleFind failed: {response.text}{Colors.NC}")
        return int((time.time() - start) * 1000), 0
    
    data = response.json()
    result_count = data.get("total", 0)
    
    
    return elapsed_ms, result_count


def main():
    print(f"\n{Colors.BLUE}{'=' * 60}{Colors.NC}")
    print(f"{Colors.BLUE}Find Performance Benchmark: Process API vs HandleFind{Colors.NC}")
    print(f"{Colors.BLUE}{'=' * 60}{Colors.NC}\n")
    
    if not os.path.exists(TEST_DIR):
        print(f"{Colors.RED}Error: Directory {TEST_DIR} does not exist{Colors.NC}")
        sys.exit(1)
    
    print(f"Repository: next.js")
    print(f"Location: {TEST_DIR}\n")
    
    results = {
        "process": {"times": [], "counts": []},
        "handler": {"times": [], "counts": []}
    }
    
    for query in QUERIES:
        print(f"{Colors.YELLOW}Pattern: '*{query}*'{Colors.NC}")
        print("-" * 60)
        
        # 1. find via process API
        print(f"  [1/2] find (process API)... ", end="", flush=True)
        process_time, process_count = run_find_via_process(query)
        results["process"]["times"].append(process_time)
        results["process"]["counts"].append(process_count)
        print(f"{Colors.GREEN}✓{Colors.NC} {format_time(process_time)} ({process_count} results)")
        
        # 2. HandleFind API
        print(f"  [2/2] HandleFind API...     ", end="", flush=True)
        handler_time, handler_count = run_handle_find(query)
        results["handler"]["times"].append(handler_time)
        results["handler"]["counts"].append(handler_count)
        print(f"{Colors.GREEN}✓{Colors.NC} {format_time(handler_time)} ({handler_count} results)")
        
        # Speedup
        if handler_time > 0:
            speedup = process_time / handler_time
            print(f"  {Colors.BLUE}→{Colors.NC} HandleFind is {speedup:.2f}x faster\n")
        else:
            print()
    
    # Summary
    print(f"{Colors.BLUE}{'=' * 60}{Colors.NC}")
    print(f"{Colors.BLUE}Summary{Colors.NC}")
    print(f"{Colors.BLUE}{'=' * 60}{Colors.NC}\n")
    
    print(f"{'Pattern':<15} | {'Process API':<15} | {'HandleFind':<15} | {'Speedup':<10}")
    print(f"{'-'*15}-+-{'-'*15}-+-{'-'*15}-+-{'-'*10}")
    
    for i, query in enumerate(QUERIES):
        process_time = format_time(results["process"]["times"][i])
        handler_time = format_time(results["handler"]["times"][i])
        if results["handler"]["times"][i] > 0:
            speedup = f"{results['process']['times'][i] / results['handler']['times'][i]:.2f}x"
        else:
            speedup = "N/A"
        print(f"*{query}*{' '*(13-len(query))} | {process_time:<15} | {handler_time:<15} | {speedup:<10}")
    
    print()
    
    # Averages
    avg_process = sum(results["process"]["times"]) / len(results["process"]["times"])
    avg_handler = sum(results["handler"]["times"]) / len(results["handler"]["times"])
    
    print(f"{Colors.YELLOW}Average Times:{Colors.NC}")
    print(f"  Process API (find):  {format_time(int(avg_process))}")
    print(f"  HandleFind API:      {format_time(int(avg_handler))}\n")
    
    if avg_handler > 0:
        avg_speedup = avg_process / avg_handler
        print(f"{Colors.GREEN}Average Speedup: {avg_speedup:.2f}x faster{Colors.NC}\n")
    
    print(f"{Colors.BLUE}Why HandleFind is faster:{Colors.NC}")
    print(f"  {Colors.GREEN}✓{Colors.NC} No process spawning overhead")
    print(f"  {Colors.GREEN}✓{Colors.NC} No waiting for process completion")
    print(f"  {Colors.GREEN}✓{Colors.NC} No log retrieval step")
    print(f"  {Colors.GREEN}✓{Colors.NC} Direct filesystem access")
    print(f"  {Colors.GREEN}✓{Colors.NC} Structured JSON response (no parsing)")
    print(f"\n{Colors.GREEN}Benchmark completed!{Colors.NC}\n")


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        print(f"\n{Colors.YELLOW}Benchmark interrupted{Colors.NC}")
        sys.exit(0)
    except Exception as e:
        print(f"\n{Colors.RED}Error: {e}{Colors.NC}")
        import traceback
        traceback.print_exc()
        sys.exit(1)
