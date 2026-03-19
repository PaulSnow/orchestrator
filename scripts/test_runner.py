#!/usr/bin/env python3
"""Test script for Go Python subprocess runner.

This script is used to verify that the Go Python helpers work correctly.
It echoes arguments, reads stdin if provided, and can simulate various exit codes.

Usage:
    python test_runner.py [args...]
    python test_runner.py --exit-code N    # Exit with code N
    python test_runner.py --echo-stdin     # Echo stdin to stdout
    python test_runner.py --to-stderr MSG  # Write MSG to stderr
    python test_runner.py --sleep N        # Sleep for N seconds
"""

import sys
import time


def main():
    args = sys.argv[1:]

    # Handle special flags
    i = 0
    while i < len(args):
        arg = args[i]

        if arg == "--exit-code" and i + 1 < len(args):
            code = int(args[i + 1])
            sys.exit(code)

        elif arg == "--echo-stdin":
            # Read and echo stdin
            content = sys.stdin.read()
            print(content, end="")
            i += 1
            continue

        elif arg == "--to-stderr" and i + 1 < len(args):
            # Write message to stderr
            print(args[i + 1], file=sys.stderr)
            i += 2
            continue

        elif arg == "--sleep" and i + 1 < len(args):
            # Sleep for specified seconds
            time.sleep(float(args[i + 1]))
            i += 2
            continue

        i += 1

    # Default behavior: echo all args
    if args:
        # Filter out the special flags for echo output
        echo_args = []
        skip_next = False
        for j, a in enumerate(args):
            if skip_next:
                skip_next = False
                continue
            if a in ("--exit-code", "--to-stderr", "--sleep"):
                skip_next = True
                continue
            if a == "--echo-stdin":
                continue
            echo_args.append(a)

        if echo_args:
            print(" ".join(echo_args))
    else:
        print("test_runner: no args")


if __name__ == "__main__":
    main()
