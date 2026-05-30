# Development

## Requirements

- Go 1.23 or newer
- Linux for networking implementation work

## Local checks

```bash
gofmt -w .
go test ./...
go run ./cmd/tunwarden version
go run ./cmd/tunwarden doctor
go run ./cmd/tunwarden panic-reset
```

## Safety rules for contributors

- Do not add networking mutations without a rollback path.
- Do not add SUID binaries.
- Do not write directly to `/etc/resolv.conf` unless it is an explicit fallback with tests and documentation.
- Do not hide route/DNS/firewall changes behind vague helper functions.
- Prefer dry-run output before execution.

## Branching

Work should go through pull requests. Keep changes small enough that networking behavior can be reviewed precisely.
