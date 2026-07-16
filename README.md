# sftp2sftp

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

```bash
sftp2sftp \
  --source user@host1:2022/path/to/server \
  --dest   user@host2:2022/path/to/server \
  --exclude "cache/,logs/" \
  --concurrency 4 \
  --resume
```

### Authentication

- `--source-key` / `--dest-key` — path to SSH private key
- If no key flag is set, tries `~/.ssh/id_ed25519` then `~/.ssh/id_rsa`
- Otherwise prompts for password on stderr (not argv)
- Encrypted keys prompt for passphrase

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `--source` | *(required)* | Source endpoint: `user@host:port/path` |
| `--dest` | *(required)* | Destination endpoint |
| `--source-key` | auto | SSH private key for source |
| `--dest-key` | auto | SSH private key for destination |
| `--exclude` | | Comma-separated exclude patterns |
| `--concurrency` | `4` | Parallel file transfers |
| `--chunk-size` | `65536` | Stream buffer size (bytes) |
| `--resume` | `false` | Skip completed files via state file |
| `--state-file` | `.sftp2sftp-state.json` | Resume state path |
| `--verify` | | `size` or `md5` post-transfer verification |
| `--no-mc-defaults` | `false` | Disable Minecraft server excludes |

### Minecraft defaults

By default these are excluded (use `--no-mc-defaults` to disable):

- `session.lock`
- `cache/` directories
- `logs/` directories
- `*.log` files

## How it works

1. Opens dual SSH/SFTP sessions (source + dest) with keepalive packets
2. Walks the source tree and builds a manifest (path, size, mode) for real progress %
3. Transfers files through an in-memory pipe (`io.CopyBuffer`) — never touches local disk
4. Creates destination parent directories as needed (`mkdir -p` equivalent)
5. Runs a worker pool (`--concurrency`) for parallel small-file throughput
6. Retries each file up to 3× with exponential backoff
7. Reconnects both sessions on connection drops and resumes from state

### Resume

With `--resume`, completed files are tracked in `.sftp2sftp-state.json` by relative path + size. On restart, fully transferred files are skipped; partial destination files are removed and re-transferred.

## Examples

```bash
# Basic migration
sftp2sftp --source mc@old-vps:22/home/mc/server --dest mc@new-vps:22/home/mc/server

# Custom keys, 8 parallel streams, verify sizes
sftp2sftp \
  --source mc@10.0.0.1:2222/opt/minecraft \
  --dest mc@10.0.0.2:2222/opt/minecraft \
  --source-key ~/.ssh/old_vps \
  --dest-key ~/.ssh/new_vps \
  --concurrency 8 \
  --verify size

# Resume interrupted transfer
sftp2sftp --source ... --dest ... --resume
```

## License

MIT
