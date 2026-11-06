#!/usr/bin/env bash
set -u
cd /home/hat47x/eki-vs
PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin ./scripts/verify.sh > /tmp/vs2.log 2>&1
echo "rc=$?"
grep -nE 'VERIFY|^FAIL|--- FAIL|== ' /tmp/vs2.log | head -30
echo "--- skip summary ---"
grep -nE '実行した試験|skip した試験' /tmp/vs2.log
