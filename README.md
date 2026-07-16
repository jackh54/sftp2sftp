# sftp2sftp

[![CI](https://github.com/jackh54/sftp2sftp/actions/workflows/ci.yml/badge.svg)](https://github.com/jackh54/sftp2sftp/actions/workflows/ci.yml)

Direct **SFTP-to-SFTP** file transfer CLI. Streams each file from source to destination over SSH — nothing is written to local disk except an optional resume state file.

Built with Go for a single static binary that runs on Linux, macOS, and Windows.

## Install

### Download (recommended)

Grab the binary for your OS from the [latest GitHub release](https://github.com/jackh54/sftp2sftp/releases/latest):

| OS | File |
|---|---|
| Windows | `sftp2sftp-windows-amd64.exe` |
| Linux | `sftp2sftp-linux-amd64` or `sftp2sftp-linux-arm64` |
| macOS | `sftp2sftp-darwin-amd64` or `sftp2sftp-darwin-arm64` (Apple Silicon) |

### Build from source

```bash
go build -o sftp2sftp ./cmd/sftp2sftp/
```

Or cross-compile all platforms:

```bash
make build-all
```

Release binaries are built by CI when you push a tag, publish a GitHub release, or run the **Release** workflow manually.

## Usage

The binary is interactive — run it in a terminal and answer the prompts. No flags required.

### Windows (PowerShell or Command Prompt)

1. Download `sftp2sftp-windows-amd64.exe` from Releases.
2. Put it somewhere easy to find (e.g. `Downloads` or a folder on your Desktop).
3. Open **PowerShell** or **Command Prompt** in that folder:
   - In File Explorer, click the address bar, type `powershell` or `cmd`, and press Enter  
   - Or Shift+right-click the folder → **Open in Terminal** / **Open PowerShell window here**
4. Run it:

**PowerShell:**

```powershell
.\sftp2sftp-windows-amd64.exe
```

**Command Prompt (cmd.exe):**

```bat
sftp2sftp-windows-amd64.exe
```

You can rename the file to `sftp2sftp.exe` first if you want a shorter command (`.\sftp2sftp.exe` in PowerShell, `sftp2sftp.exe` in cmd).

> Double-clicking the `.exe` also works, but a console window may close on errors — running from PowerShell/cmd is clearer.

### Linux / macOS

```bash
chmod +x sftp2sftp-linux-amd64   # once, after download
./sftp2sftp-linux-amd64
```

(Use the darwin binary name on macOS.)

### What you'll be asked

1. **Connection** — source and destination as Pterodactyl-style SFTP URLs
2. **Authentication** — password by default; SSH private keys are an optional choice
3. **Transfer** — parallelism, resume, verification mode
4. **Excludes** — Minecraft defaults and custom patterns
5. **Confirm** — review summary, connect, then browse the source to select files/folders


### Endpoint format

Paste the **Launch SFTP** URL from a Pterodactyl panel (Settings → SFTP Details):

```text
sftp://username.serverid@hostname:2022
```

Optional path selects a subdirectory (defaults to `/`, the SFTP jail root):

```text
sftp://username.serverid@hostname:2022/plugins
```

Legacy `user@host:port/path` strings are still accepted.

### Authentication

- **Password** is the default (for Pterodactyl, use your panel password)
- **SSH private key** is available as an explicit option; you provide the key path per host
- Passwords are never passed on argv
- Encrypted keys prompt for passphrase

### Minecraft defaults

When enabled in the wizard, these are excluded:

- `session.lock`
- `cache/` directories
- `logs/` directories
- `*.log` files

## How it works

1. Opens dual SSH/SFTP sessions (source + dest) with keepalive packets
2. Browse the source tree interactively and select files/folders to copy (live SFTP listing — no full-tree scan upfront)
3. Expands selected folders, then transfers files through an in-memory pipe (`io.CopyBuffer`) — never touches local disk
4. Creates destination parent directories as needed (`mkdir -p` equivalent)
5. Runs a worker pool for parallel small-file throughput
6. Retries each file up to 3× with exponential backoff
7. Reconnects both sessions on connection drops and resumes from state

### Resume

If you choose resume in the wizard, completed files are tracked in `.sftp2sftp-state.json` by relative path + size. On restart, fully transferred files are skipped; partial destination files are removed and re-transferred.

## License

MIT
