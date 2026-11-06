#!/usr/bin/env bash
set -u
cd /root/eki-vs 2>/dev/null || cd "$(ls -d /home/*/eki-vs 2>/dev/null | head -1)"
pwd
PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin ./scripts/verify.sh > /tmp/vs.log 2>&1
echo "rc=$?"
grep -n 'VERIFY|FAIL|== ' /tmp/vs.log | head -30
