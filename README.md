# devhostd

devhostd gives development servers stable, named URLs such as `https://myapp.localhost`. A single background daemon terminates TLS and proxies requests to registered applications. Per-app runners allocate ports, register routes, and manage child processes.

## Highlights

- Stable HTTPS names with no port in the normal URL
- Automatic local CA and per-host certificate generation
- HTTP/1.1, HTTP/2, WebSocket, SSE, and streaming proxy support
- Dynamic runners and persistent aliases
- Automatic port selection and process cleanup
- Multiple TLDs, wildcard subdomains, custom certificates, and hosts-file synchronization
- IPv4 and IPv6 loopback support
- Optional LAN mode with `.local` mDNS publication
- Monorepo app maps and package-workspace discovery
- Tailscale Serve, Tailscale Funnel, and ngrok sharing
- JSON output and a local agent-facing control API
- launchd, systemd, and Windows Task Scheduler service integration

## Install

Install the latest macOS or Linux release directly from GitHub:

```sh
curl -fsSL https://raw.githubusercontent.com/patriceckhart/devhostd/main/install.sh | bash
```

The installer detects the operating system and architecture, downloads the matching GitHub release, verifies its SHA-256 checksum, and installs `devhostd` into the first writable directory among `/usr/local/bin`, `~/.local/bin`, and `~/bin`.

Pin a version and optionally choose an installation directory:

```sh
curl -fsSL https://raw.githubusercontent.com/patriceckhart/devhostd/main/install.sh | bash -s -- v0.0.1 ~/bin
```

Environment variables can provide the same overrides:

```sh
curl -fsSL https://raw.githubusercontent.com/patriceckhart/devhostd/main/install.sh \
  | DEVHOSTD_VERSION=v0.0.1 DEVHOSTD_PREFIX="$HOME/bin" bash
```

Windows users can download the `.zip` archive from the [GitHub releases page](https://github.com/patriceckhart/devhostd/releases).

## Requirements

Building requires Go 1.25 or newer. Tailscale and ngrok sharing require their respective command-line programs at runtime.

## Build

Local builds default to version `0.0.0`. Override `VERSION` to embed another version:

```sh
make build
make build VERSION=0.0.7
make install
```

Create unpackaged cross-platform binaries locally with:

```sh
make release VERSION=0.0.7
```

Create release archives and checksums through GoReleaser:

```sh
goreleaser release --snapshot --clean
```

## Automated releases

Every successful CI run on `main` creates a tagged release unless the commit message contains `[release=skip]`. Versions begin at `v0.0.1`, increase automatically, and roll over after patch 99:

```text
v0.0.1 ... v0.0.99 -> v0.1.0 ... v0.1.99 -> v0.2.0
```

The release contains archives and SHA-256 checksums for macOS, Linux, and Windows on supported amd64 and arm64 targets. The tagged version is embedded in the binary and shown by `devhostd version`.

## Quick start

Start the daemon, then run an application through devhostd:

```sh
devhostd daemon start
devhostd run myapp -- npm run dev
```

The application is available at:

```text
https://myapp.localhost
```

### Next.js examples

Run the Next.js CLI directly with Bun:

```sh
devhostd run web -- bun next dev
```

With npm, run the project's `dev` script:

```sh
devhostd run web -- npm run dev
```

You can also invoke Next.js directly through npm's package runner:

```sh
devhostd run web -- npx next dev
```

Each example receives its application port through the `PORT` environment variable and is available at `https://web.localhost`.

The daemon defaults to HTTPS on port 443. It elevates privileges when a privileged port or system file update requires it. On the first interactive TLS startup, devhostd offers to install its generated CA into the system trust store. You can also install trust explicitly:

```sh
devhostd trust
```

For an application that is already running:

```sh
devhostd alias api 4000
devhostd list
devhostd alias --remove api
```

For CI or an unprivileged smoke test:

```sh
DEVHOSTD_STATE_DIR=$(mktemp -d) \
  devhostd daemon start --port 8080 --no-tls
```

## Commands

```text
devhostd run [name] [--app-port <port>] [--force] -- <command...>
devhostd alias <name> <port> [--force]
devhostd alias --remove <name>
devhostd list [--json]
devhostd status [--json]
devhostd daemon start [--foreground] [--port <port>] [--no-tls]
                      [--tld <tld>]... [--wildcard] [--lan]
                      [--cert <path>] [--key <path>]
devhostd daemon stop
devhostd trust
devhostd hosts sync
devhostd hosts clean
devhostd service install [daemon options...]
devhostd service status [--json]
devhostd service uninstall
devhostd doctor [--json]
devhostd prune
devhostd share tailscale <name> [--public]
devhostd share tailscale <name> [--public] --stop
devhostd share ngrok <name>
devhostd api <method> [json-params]
devhostd update [--check]
devhostd clean
devhostd version
```

Run `devhostd help` for the built-in summary.

## Project configuration

A `devhostd.json` file can provide runner defaults:

```json
{
  "name": "myapp",
  "script": "dev",
  "appPort": 0
}
```

The same configuration can be placed under the `devhostd` key in `package.json`:

```json
{
  "scripts": {
    "dev": "vite"
  },
  "devhostd": {
    "name": "myapp",
    "script": "dev"
  }
}
```

Configuration precedence is:

1. CLI options
2. Environment variables
3. The nearest `devhostd.json`
4. The `devhostd` key in the nearest `package.json`
5. Built-in defaults

When no explicit command follows `--`, devhostd runs the configured script with the package manager selected from the repository lockfile.

## Monorepos

A root configuration can map applications by relative directory:

```json
{
  "name": "platform",
  "apps": {
    "apps/web": {
      "name": "web",
      "script": "dev"
    },
    "apps/api": {
      "name": "api",
      "script": "start",
      "appPort": 4000
    }
  }
}
```

Running inside `apps/web` produces `web.platform.localhost`. If no `apps` map exists, devhostd discovers package-manager workspaces and derives the same `<package>.<project>.localhost` naming convention from package names.

## Runner environment

Child processes receive:

```text
PORT=<allocated or configured application port>
HOST=127.0.0.1
DEVHOSTD_URL=<public application URL>
```

With generated TLS enabled, Node.js processes also receive `NODE_EXTRA_CA_CERTS` pointing to the devhostd root CA.

Signals and exit codes are forwarded to the child process. Routes are removed when the runner exits.

## LAN mode

LAN mode listens on local network interfaces, adds `.local` hostnames, and answers mDNS queries for active routes:

```sh
devhostd daemon start --lan
devhostd run myapp -- npm run dev
```

Other devices can resolve `myapp.local` when multicast DNS is available on the network. HTTPS clients on those devices must trust the devhostd root CA, or the daemon must use an appropriate custom certificate.

## Sharing

Publish a route to your Tailscale network:

```sh
devhostd share tailscale myapp
```

Use Funnel for public access and stop either mode when finished:

```sh
devhostd share tailscale myapp --public
devhostd share tailscale myapp --public --stop
```

Publish through ngrok. The command remains attached to the ngrok process until it is interrupted:

```sh
devhostd share ngrok myapp
```

The public Tailscale or ngrok hostname is registered dynamically with the daemon so Host-based routing and generated certificates continue to work.

## Agent-facing API

The local control socket uses newline-delimited JSON. The CLI provides a convenient caller:

```sh
devhostd api health
devhostd api routes
devhostd api route '{"name":"myapp"}'
devhostd api config
```

Useful methods include `ping`, `status`, `health`, `list`, `routes`, `route`, `config`, `register`, `deregister`, `add_hostname`, and `remove_hostname`. The API is local-only through a Unix domain socket or Windows named pipe.

## Updates

Released builds check GitHub for a newer version when the CLI is used. Results are cached for 12 hours, network failures are ignored, and an available release prints a short notice to standard error:

```text
devhostd 0.0.4 is available (current: 0.0.3). Run `devhostd update`.
```

Check explicitly without installing anything:

```sh
devhostd update --check
```

Download the matching release archive, verify its SHA-256 checksum, and replace the current executable:

```sh
devhostd update
```

The executable must be writable by the current user. A system-owned installation may require running the update with elevated privileges. Development builds report `dev` or `0.0.0` and cannot update themselves. Private repository releases use `GITHUB_TOKEN` when it is available.

Set `DEVHOSTD_UPDATE_CHECK=0` to disable automatic checks. The explicit `devhostd update` commands remain available.

## Environment variables

```text
DEVHOSTD=0                 Bypass devhostd in `run`
DEVHOSTD_PORT=<port>       Proxy listener port
DEVHOSTD_APP_PORT=<port>   Application listener port
DEVHOSTD_HTTPS=0           Disable TLS
DEVHOSTD_TLD=a[,b]         Configure one or more TLDs
DEVHOSTD_WILDCARD=1        Enable wildcard route matching
DEVHOSTD_LAN=1             Enable LAN listening and `.local` mDNS
DEVHOSTD_SYNC_HOSTS=0      Disable automatic hosts-file synchronization
DEVHOSTD_UPDATE_CHECK=0    Disable automatic release checks
DEVHOSTD_STATE_DIR=<path>  Override the state directory
```

CLI options take precedence over environment variables.

## Services and diagnostics

Install the daemon as a native startup service:

```sh
devhostd service install
devhostd service status
devhostd service uninstall
```

Run diagnostics and remove stale routes:

```sh
devhostd doctor
devhostd doctor --json
devhostd prune
```

`devhostd clean` stops the daemon and removes managed state, hosts entries, and the CA trust entry.

## Development

```sh
make fmt
make test
make lint
```

## License

MIT
