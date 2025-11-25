#!/usr/bin/env python3
"""
Benchmark: Filesystem List API
Runs 2000 calls and measures average response time with statistics
"""

import os
import sys
import time
import statistics
from typing import List

import requests

# Configuration
API_URL = os.getenv("API_BASE_URL", "http://localhost:8080")
TEST_DIR = "/Users/mjoffre/Documents/blaxel/sandbox/hub/"
NUM_CALLS = 2000

# Colors
class Colors:
    RED = '\033[0;31m'
    GREEN = '\033[0;32m'
    YELLOW = '\033[1;33m'
    BLUE = '\033[0;34m'
    CYAN = '\033[0;36m'
    NC = '\033[0m'


def format_time(ms: float) -> str:
    """Format time in milliseconds"""
    if ms < 1000:
        return f"{ms:.2f}ms"
    else:
        return f"{ms/1000:.2f}s"


def run_filesystem_list() -> float:
    """Run filesystem list API call and return elapsed time in ms"""
    start = time.time()
    
    # Encode path: /Users/... -> %2FUsers%2F...
    encoded_path = TEST_DIR.replace("/", "%2F")
    
    response = requests.get(
        f"{API_URL}/filesystem/{encoded_path}",
        timeout=30
    )
    elapsed_ms = (time.time() - start) * 1000
    
    if response.status_code != 200:
        raise Exception(f"Request failed: {response.status_code} - {response.text}")
    
    return elapsed_ms


def calculate_percentile(data: List[float], percentile: int) -> float:
    """Calculate percentile of data"""
    sorted_data = sorted(data)
    index = (percentile / 100.0) * (len(sorted_data) - 1)
    lower = int(index)
    upper = lower + 1
    weight = index - lower
    
    if upper >= len(sorted_data):
        return sorted_data[lower]
    
    return sorted_data[lower] * (1 - weight) + sorted_data[upper] * weight


def main():
    print(f"\n{Colors.BLUE}{'=' * 70}{Colors.NC}")
    print(f"{Colors.BLUE}Filesystem List API - Performance Benchmark{Colors.NC}")
    print(f"{Colors.BLUE}{'=' * 70}{Colors.NC}\n")
    
    if not os.path.exists(TEST_DIR):
        print(f"{Colors.RED}Error: Directory {TEST_DIR} does not exist{Colors.NC}")
        sys.exit(1)
    
    print(f"Target Directory: {Colors.CYAN}{TEST_DIR}{Colors.NC}")
    print(f"API URL:          {Colors.CYAN}{API_URL}{Colors.NC}")
    print(f"Number of Calls:  {Colors.CYAN}{NUM_CALLS}{Colors.NC}\n")
    
    # Warmup
    print(f"{Colors.YELLOW}Warming up...{Colors.NC}")
    try:
        warmup_time = run_filesystem_list()
        print(f"  {Colors.GREEN}✓{Colors.NC} Warmup completed: {format_time(warmup_time)}\n")
    except Exception as e:
        print(f"{Colors.RED}✗ Warmup failed: {e}{Colors.NC}")
        sys.exit(1)
    
    # Run benchmark
    print(f"{Colors.YELLOW}Running {NUM_CALLS} requests...{Colors.NC}")
    times: List[float] = []
    errors = 0
    
    start_time = time.time()
    for i in range(NUM_CALLS):
        if (i + 1) % 100 == 0:
            progress = ((i + 1) / NUM_CALLS) * 100
            elapsed = time.time() - start_time
            rate = (i + 1) / elapsed if elapsed > 0 else 0
            print(f"  Progress: {i + 1}/{NUM_CALLS} ({progress:.1f}%) - {rate:.1f} req/s", end='\r', flush=True)
        
        try:
            elapsed_ms = run_filesystem_list()
            times.append(elapsed_ms)
        except Exception as e:
            errors += 1
            if errors <= 5:  # Only print first 5 errors
                print(f"\n{Colors.RED}  Error #{errors}: {e}{Colors.NC}")
    
    total_time = time.time() - start_time
    print()  # New line after progress
    
    if len(times) == 0:
        print(f"\n{Colors.RED}All requests failed!{Colors.NC}\n")
        sys.exit(1)
    
    # Calculate statistics
    avg_time = statistics.mean(times)
    median_time = statistics.median(times)
    min_time = min(times)
    max_time = max(times)
    stdev_time = statistics.stdev(times) if len(times) > 1 else 0
    p50 = calculate_percentile(times, 50)
    p95 = calculate_percentile(times, 95)
    p99 = calculate_percentile(times, 99)
    requests_per_sec = NUM_CALLS / total_time
    
    # Display results
    print(f"\n{Colors.BLUE}{'=' * 70}{Colors.NC}")
    print(f"{Colors.BLUE}Results{Colors.NC}")
    print(f"{Colors.BLUE}{'=' * 70}{Colors.NC}\n")
    
    print(f"{Colors.YELLOW}Timing Statistics:{Colors.NC}")
    print(f"  Total Requests:    {Colors.GREEN}{len(times)}{Colors.NC} successful, {Colors.RED}{errors}{Colors.NC} failed")
    print(f"  Total Time:        {Colors.CYAN}{format_time(total_time * 1000)}{Colors.NC}")
    print(f"  Throughput:        {Colors.CYAN}{requests_per_sec:.2f} req/s{Colors.NC}\n")
    
    print(f"{Colors.YELLOW}Response Time:{Colors.NC}")
    print(f"  Average:           {Colors.GREEN}{format_time(avg_time)}{Colors.NC}")
    print(f"  Median:            {Colors.GREEN}{format_time(median_time)}{Colors.NC}")
    print(f"  Min:               {Colors.GREEN}{format_time(min_time)}{Colors.NC}")
    print(f"  Max:               {Colors.GREEN}{format_time(max_time)}{Colors.NC}")
    print(f"  Std Dev:           {Colors.CYAN}{format_time(stdev_time)}{Colors.NC}\n")
    
    print(f"{Colors.YELLOW}Percentiles:{Colors.NC}")
    print(f"  P50 (median):      {Colors.GREEN}{format_time(p50)}{Colors.NC}")
    print(f"  P95:               {Colors.GREEN}{format_time(p95)}{Colors.NC}")
    print(f"  P99:               {Colors.GREEN}{format_time(p99)}{Colors.NC}\n")
    
    # Distribution
    print(f"{Colors.YELLOW}Response Time Distribution:{Colors.NC}")
    buckets = [
        (0, 1, "< 1ms"),
        (1, 2, "1-2ms"),
        (2, 3, "2-3ms"),
        (3, 5, "3-5ms"),
        (5, 10, "5-10ms"),
        (10, 20, "10-20ms"),
        (20, 30, "20-30ms"),
        (30, 50, "30-50ms"),
        (50, 75, "50-75ms"),
        (75, 100, "75-100ms"),
        (100, 150, "100-150ms"),
        (150, 200, "150-200ms"),
        (200, 300, "200-300ms"),
        (300, 500, "300-500ms"),
        (500, 1000, "500ms-1s"),
        (1000, float('inf'), "> 1s")
    ]
    
    for min_val, max_val, label in buckets:
        count = sum(1 for t in times if min_val <= t < max_val)
        percentage = (count / len(times)) * 100
        bar_length = int(percentage / 2)  # Scale bar to fit terminal
        bar = "█" * bar_length
        print(f"  {label:<12} {bar} {count:>4} ({percentage:>5.1f}%)")
    
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
