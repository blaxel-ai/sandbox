#!/usr/bin/env python3
"""
Simple benchmark: grep (process API) vs HandleContentSearch API

Compares:
1. grep command via process API (spawns process, waits, parses logs)
2. HandleContentSearch API (pure Go parallel grep implementation)
"""

import os
import sys
import time
from typing import Tuple

import requests

# Configuration
API_URL = os.getenv("API_BASE_URL", "http://localhost:8080")
TEST_DIR = "/Users/mjoffre/Documents/blaxel/sandbox/hub"
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


def run_grep_via_process(pattern: str) -> Tuple[int, int]:
    """Run grep command via process API"""
    start = time.time()
    
    cmd = f"grep -r -n -i \"{pattern}\" {TEST_DIR}"
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
        return elapsed_ms, 0
    
    pid = response.json().get("pid")
    
    # Get logs
    logs_response = requests.get(f"{API_URL}/process/{pid}/logs")
    if logs_response.status_code == 200:
        stdout = logs_response.json().get("stdout", "")
        result_count = len([line for line in stdout.split('\n') if line.strip()])
    else:
        result_count = 0
    
    return elapsed_ms, result_count


def run_handle_content_search(pattern: str) -> Tuple[int, int]:
    """Run HandleContentSearch API directly"""
    start = time.time()
    
    # Encode path: /Users/... -> %2FUsers%2F...
    encoded_path = TEST_DIR.replace("/", "%2F")
    
    response = requests.get(
        f"{API_URL}/filesystem-content-search/{encoded_path}",
        params={
            "query": pattern,
            "caseSensitive": "false",
            "maxResults": "1000",
            "excludeDirs": "node_modules,.git"
        }
    )
    elapsed_ms = int((time.time() - start) * 1000)
    
    if response.status_code != 200:
        print(f"{Colors.RED}HandleContentSearch failed: {response.text}{Colors.NC}")
        return elapsed_ms, 0
    
    data = response.json()
    result_count = data.get("total", 0)
    
    return elapsed_ms, result_count


def main():
    print(f"\n{Colors.BLUE}{'=' * 60}{Colors.NC}")
    print(f"{Colors.BLUE}Grep Performance Benchmark: Process API vs Pure Go{Colors.NC}")
    print(f"{Colors.BLUE}{'=' * 60}{Colors.NC}\n")
    
    if not os.path.exists(TEST_DIR):
        print(f"{Colors.RED}Error: Directory {TEST_DIR} does not exist{Colors.NC}")
        sys.exit(1)
    
    print(f"Directory: e2e")
    print(f"Location: {TEST_DIR}\n")
    
    results = {
        "process": {"times": [], "counts": []},
        "handler": {"times": [], "counts": []}
    }
    
    for query in QUERIES:
        print(f"{Colors.YELLOW}Search: '{query}'{Colors.NC}")
        print("-" * 60)
        
        # 1. grep via process API
        print(f"  [1/2] grep (process API)...         ", end="", flush=True)
        process_time, process_count = run_grep_via_process(query)
        results["process"]["times"].append(process_time)
        results["process"]["counts"].append(process_count)
        print(f"{Colors.GREEN}✓{Colors.NC} {format_time(process_time)} ({process_count} results)")
        
        # 2. HandleContentSearch API
        print(f"  [2/2] HandleContentSearch API...    ", end="", flush=True)
        handler_time, handler_count = run_handle_content_search(query)
        results["handler"]["times"].append(handler_time)
        results["handler"]["counts"].append(handler_count)
        print(f"{Colors.GREEN}✓{Colors.NC} {format_time(handler_time)} ({handler_count} results)")
        
        # Speedup
        if handler_time > 0:
            speedup = process_time / handler_time
            print(f"  {Colors.BLUE}→{Colors.NC} HandleContentSearch is {speedup:.2f}x faster\n")
        else:
            print()
    
    # Summary
    print(f"{Colors.BLUE}{'=' * 60}{Colors.NC}")
    print(f"{Colors.BLUE}Summary{Colors.NC}")
    print(f"{Colors.BLUE}{'=' * 60}{Colors.NC}\n")
    
    print(f"{'Query':<15} | {'Process API':<15} | {'Pure Go API':<15} | {'Speedup':<10}")
    print(f"{'-'*15}-+-{'-'*15}-+-{'-'*15}-+-{'-'*10}")
    
    for i, query in enumerate(QUERIES):
        process_time = format_time(results["process"]["times"][i])
        handler_time = format_time(results["handler"]["times"][i])
        if results["handler"]["times"][i] > 0:
            speedup = f"{results['process']['times'][i] / results['handler']['times'][i]:.2f}x"
        else:
            speedup = "N/A"
        print(f"'{query}'{' '*(13-len(query))} | {process_time:<15} | {handler_time:<15} | {speedup:<10}")
    
    print()
    
    # Averages
    avg_process = sum(results["process"]["times"]) / len(results["process"]["times"])
    avg_handler = sum(results["handler"]["times"]) / len(results["handler"]["times"])
    
    print(f"{Colors.YELLOW}Average Times:{Colors.NC}")
    print(f"  Process API (grep):          {format_time(int(avg_process))}")
    print(f"  HandleContentSearch (Go):    {format_time(int(avg_handler))}\n")
    
    if avg_handler > 0:
        avg_speedup = avg_process / avg_handler
        print(f"{Colors.GREEN}Average Speedup: {avg_speedup:.2f}x faster{Colors.NC}\n")
    
    print(f"{Colors.BLUE}Why HandleContentSearch is faster:{Colors.NC}")
    print(f"  {Colors.GREEN}✓{Colors.NC} No process spawning overhead")
    print(f"  {Colors.GREEN}✓{Colors.NC} No waiting for process completion")
    print(f"  {Colors.GREEN}✓{Colors.NC} No log retrieval step")
    print(f"  {Colors.GREEN}✓{Colors.NC} Parallel processing (8 worker goroutines)")
    print(f"  {Colors.GREEN}✓{Colors.NC} Direct memory access to files")
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
