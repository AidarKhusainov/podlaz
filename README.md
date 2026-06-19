# podlaz — Linux VPN client

A quiet way through.

podlaz is a Linux VPN client with profile management and a privileged daemon.

## Features

- Profile management for Xray-compatible profiles.
- Explicit, inspectable, and recoverable profile lifecycle behavior.
- CLI-first workflow with a privileged daemon for runtime operations.

## Build

```bash
go test ./...
go run ./cmd/podlaz version
go run ./cmd/podlazd
```

Build a local Debian package:

```bash
bash scripts/build-deb.sh
sudo apt install ./dist/podlaz_0.0.0~dev-1_linux_amd64.deb
```

## Documentation

Start with [Documentation](docs/README.md).

## Repository

https://github.com/AidarKhusainov/podlaz

## License

podlaz is licensed under the MIT License. See [LICENSE](LICENSE).
