# sftp2sftp

[![CI](https://github.com/jackh54/sftp2sftp/actions/workflows/ci.yml/badge.svg)](https://github.com/jackh54/sftp2sftp/actions/workflows/ci.yml)

Direct **SFTP-to-SFTP** file transfer CLI. Streams each file from source to destination over SSH — nothing is written to local disk except an optional resume state file.

Built with Go for a single static binary that runs on Linux, macOS, and Windows.

## Install

```bash
go build -o sftp2sftp ./cmd/sftp2sftp/
```

Or cross-compile:

```bash
make build-all
```

## Usage

Run the binary — it launches an interactive setup wizard:

```bash
sftp2sftp
```

You'll be prompted for:

1. **Connection** — source and destination (`user@host:port/path`)
2. **Authentication** — optional SSH key paths (blank = auto-detect `~/.ssh/id_*`, then password prompt)
3. **Transfer** — parallelism, resume, verification mode
4. **Excludes** — Minecraft defaults and custom patterns
5. **Confirm** — review summary, then transfer starts

No flags required.

### Authentication

- Optional SSH key path per host
- If blank, tries `~/.ssh/id_ed25519` then `~/.ssh/id_rsa`
- Otherwise prompts for password (never on argv)
- Encrypted keys prompt for passphrase

### Minecraft defaults

When enabled in the wizard, these are excluded:

- `session.lock`
- `cache/` directories
- `logs/` directories
- `*.log` files

## How it works

1. Opens dual SSH/SFTP sessions (source + dest) with keepalive packets
2. Walks the source tree and builds a manifest (path, size, mode) for real progress %
3. Transfers files through an in-memory pipe (`io.CopyBuffer`) — never touches local disk
4. Creates destination parent directories as needed (`mkdir -p` equivalent)
5. Runs a worker pool for parallel small-file throughput
6. Retries each file up to 3× with exponential backoff
7. Reconnects both sessions on connection drops and resumes from state

### Resume

If you choose resume in the wizard, completed files are tracked in `.sftp2sftp-state.json` by relative path + size. On restart, fully transferred files are skipped; partial destination files are removed and re-transferred.

## License

MIT
