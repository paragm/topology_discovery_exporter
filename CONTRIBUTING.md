# Contributing

We welcome contributions to the Topology Discovery Exporter!

## Development

### Prerequisites

- Go 1.23 or later
- `golangci-lint` for linting
- `make` for build automation

### Building

```bash
make build
```

### Testing

```bash
make test
```

### Linting

```bash
make lint
```

## Submitting Changes

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/my-feature`)
3. Commit your changes (`git commit -am 'Add my feature'`)
4. Push to the branch (`git push origin feature/my-feature`)
5. Open a Pull Request

### Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):
- `feat:` new feature
- `fix:` bug fix
- `docs:` documentation
- `test:` adding or updating tests
- `chore:` maintenance

## Code Style

- Run `golangci-lint run ./...` before submitting
- Follow standard Go conventions (`gofmt`, `go vet`)
- Add tests for new functionality

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
