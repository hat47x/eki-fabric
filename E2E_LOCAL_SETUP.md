# E2E Local Setup

This document records the exact local steps to run a real EKI Control Plane
for TEI-EKI integration testing. It does not start a live service for you;
it records what to run.

## 1. Build eki-control

```powershell
go build ./cmd/eki-control
```

## 2. Create a config file

Example: `D:\GIT\hat47x\eki-fabric\artifacts\local-eki-control.json`

```json
{
  "listen": "127.0.0.1:18443",
  "database": "D:\\GIT\\hat47x\\eki-fabric\\artifacts\\local-eki-control.sqlite",
  "tls": {
    "cert_file": "D:\\GIT\\hat47x\\eki-fabric\\artifacts\\local-eki-control.crt",
    "key_file": "D:\\GIT\\hat47x\\eki-fabric\\artifacts\\local-eki-control.key"
  },
  "tokens": [
    {
      "principal": "tei-client",
      "role": "client",
      "token_sha256": "<SHA256 of bearer token>"
    }
  ],
  "policy": {
    "approved_executors": ["tei-pure-compute"],
    "per_task": {
      "cpu_millis": 1000,
      "memory_bytes": 1073741824,
      "wall_time_seconds": 60,
      "output_bytes_max": 1048576,
      "network_bytes_max": 0
    },
    "max_attempts": 2,
    "max_outstanding_tasks": 4
  }
}
```

Generate a bearer token:

```powershell
.\eki-control gen-token
```

Put the printed `sha256` into `token_sha256`. Keep the token secret for the
test run only.

## 3. Run eki-control

```powershell
.\eki-control --config .\artifacts\local-eki-control.json
```

The process logs to stderr. Stop it with Ctrl-C.

## 4. Run TEI live binding test

In `tei-platform`:

```powershell
$env:EKI_CONTROL_URL = "https://127.0.0.1:18443"
$env:EKI_CONTROL_TOKEN = "<the bearer token>"
go test ./reference/product-contracts/bindings -run TestLiveEKITaskSubmitHTTP -v
```

## 5. Expected outcome

- Task is admitted and `task_id` starts with `tsk_`.
- Replaying the same idempotency key returns the same `task_id`.
- A conflicting body with the same key returns `IDEMPOTENCY_KEY_CONFLICT`.

## Notes

- This setup is for local integration testing only.
- The SQLite file and TLS material are generated in `D:\GIT\hat47x\eki-fabric\artifacts\`.
- Do not reuse the bearer token outside this test run.
