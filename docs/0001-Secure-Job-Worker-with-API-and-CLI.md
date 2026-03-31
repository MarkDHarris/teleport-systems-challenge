---
authors: Mark Harris @MarkDHarris (mark.harris@outlook.com)
---

# RFD 0001 - Secure Job Worker Service with API and CLI

## Required Approvers

- Engineering: @rosstimothy, @greedy52, @rhammonds-teleport, @russjones

## What

Provide a secure Linux-based remote job worker service for authenticated users
to start, stop, query status, and stream the output of running arbitrary Linux
processes.

## Why

SSH is commonly used for remote task execution, but it grants broad interactive
access that is hard to lock down and audit. Wrapper scripts and container-based
approaches either inherit the same access model or introduce operational overhead
that isn't justified for running a single command.

A purpose-built daemon with a narrow API is easier to secure. You can enforce
mTLS at the transport layer, scope access per-job, and stream output without
granting a shell.

## Goals

- **Secure remote job execution** — Execute arbitrary Linux processes on a single
  host with a special purpose API. Commands are executed directly without shell
  interpretation to mitigate shell injection as an attack surface.

- **mTLS-only authentication** — Mutual TLS with TLS 1.3. Identity is
  established cryptographically at the transport layer with no additional
  authentication protocols.

- **Owner-scoped authorization** — Jobs are owned by the certificate identity
  that created them. Only the owner or an admin can access a job. Unrecognized
  identities are denied by default.

- **Complete process lifecycle management** — Start, stop, and query status of
  jobs. Stopped jobs are terminated by killing the process directly.

- **Efficient and binary-safe output streaming** — Replay all output from the start
  of execution and follow live output without busy-waiting or polling.
  No assumptions about output encoding by streaming as raw bytes.

- **Concurrent client support** — Multiple independent clients can query status
  and stream output for the same job simultaneously, each with an independent
  read position.

- **Separation of concerns** — Isolate process management logic (library) from
  transport and authentication (gRPC server) and user interaction (CLI).

- **Correctness under concurrency** — The goal is no data races, no goroutine
  leaks, and no deadlocks. All shared state will be protected by explicit
  synchronization primitives and validated with Go's race detector (`go test
  -race`).

## Non-Goals

- **Distributed execution or orchestration** — The system runs on a single host.
  No cross-node scheduling, coordination, or service discovery.

- **High availability or fault tolerance** — No clustering, replication, or
  failover. A server restart loses all job state.

- **Interactive shell** — This is a batch job execution service, not a remote
  terminal. No stdin forwarding.

- **Shell command interpretation** — Commands are executed directly via `exec`,
  not through a shell. Clients who need shell features must use `/bin/sh -c "..."`
  as the argument.

- **Performance optimization or scaling** — No connection pooling, caching, or load
  balancing.

- **Dynamic policy or delegation** — The admin designation is a static check on
  the certificate OU. There is no runtime policy reload, group-based access, or
  delegated sharing.

- **Persistent job history or log retention** — Job state and output exist only
  in memory for the lifetime of the server process.

- **Full configurability** — Hardcoded defaults are preferred over configuration
  files or environment variable parsing. TODOs will document.

- **Process group termination** - This design explicitly does not include process
  group management or hierarchical termination of child processes. As a result,
  Stop() guarantees termination of the immediate process started by exec.Cmd,
  but does not ensure cleanup of any subprocesses it may have spawned.

## Design

The implementation separates concerns across three components:

1. **Worker library** — process lifecycle management and output capture, with no
   dependency on networking or authentication.

2. **gRPC server** — exposes the library over mTLS-secured gRPC with owner-scoped
   authorization enforced via interceptors and per-request handler checks.

3. **CLI client** — command-line tool built with [spf13/cobra](https://github.com/spf13/cobra)
   that communicates with the gRPC server.

### Worker Library

The library manages jobs through two core types: `Job` and `JobManager`.

**JobManager** owns a `map[string]*Job` protected by a `sync.RWMutex`. It
handles job creation, lookup by ID, and cancellation. Job status and stream
operations take a read lock; job start and job state transitions take a write
lock.  I chose `sync.RWMutex` over Go's `sync.Map` here because the Go
[type Map struct](https://pkg.go.dev/sync#Map) docs recommend a
plain map with a mutex for most use cases, and RWMutex allows concurrent status
checks without blocking each other.

**Job** represents a single process. Each job holds:

- An `exec.Cmd` for the spawned process.
- A lifecycle state machine: `Running -> Completed | Failed | Stopped`.
- An `OutputBuffer` - an append-only `[]byte` with a `sync.Cond` for
  notification.
- A `context.CancelFunc` used by `Stop()` to trigger process termination.
  A standalone context is created per job with `context.WithCancel`. A dedicated
  goroutine blocks on `<-ctx.Done()` and calls `cmd.Process.Kill()` when it
  fires.
- An `Owner` string — the certificate CN of the client that created the job.
  Set once at creation time and never modified.

When a job is created (via `CreateJob`), the manager:

1. Generates a UUID for the job.
2. Stores the job in the map so it is immediately discoverable by other RPCs.
3. Creates an `exec.Cmd` from the argv (the first element is the
   executable, the remaining are arguments).
4. Connects `cmd.Stdout` and `cmd.Stderr` to the output buffer.
5. Starts the process.
6. Launches a goroutine that calls `cmd.Wait()` and transitions the state to
   `Completed` (exit 0), `Failed` (non-zero exit), or `Stopped` (cancelled by
   user via `CancelJob`).
7. Returns the ID.

### Output Streaming

Process output is captured in an append-only in-memory byte buffer. Each write
appends to the buffer under a mutex, then calls `sync.Cond.Broadcast()` to wake
all waiting readers.

Each streaming client maintains an independent byte offset into the buffer. When
a client connects, it starts reading from offset 0 (replaying historical
output), then follows live appends. When the reader's offset equals the buffer
length and the job is still running, the reader calls `sync.Cond.Wait()`, which
suspends the goroutine until the next `Broadcast()`.

I chose `sync.Cond` over a channel-per-subscriber model after reading the
[sync.Cond documentation](https://pkg.go.dev/sync#Cond). A channel fan-out
would require the writer to maintain and iterate a subscriber list, while
`Broadcast()` wakes all waiters with a single call and no risk of blocking
the writer on a slow consumer.

One concern I need to validate is that `sync.Cond.Wait()` does not have a built-in
awareness of context cancellation. If a gRPC client disconnects, the goroutine blocked on
`Wait()` won't wake up on its own. Go 1.21 added
[`context.AfterFunc`](https://pkg.go.dev/context#AfterFunc), which solves this
by registering a callback that fires when the context is done, and
returns a stop function for cleanup. The pattern looks like:

```go
stopf := context.AfterFunc(ctx, func() {
    cond.L.Lock()
    defer cond.L.Unlock()
    cond.Broadcast()
})
defer stopf()
```

This wakes the blocked reader on cancellation; the reader then checks
`ctx.Err()` and exits. I found this approach in a
[Stack Overflow thread](https://stackoverflow.com/questions/29923666/waiting-on-a-sync-cond-with-a-timeout)
and confirmed it uses only the standard library. The existence of third-party
wrappers like [Jille/contextcond](https://github.com/Jille/contextcond)
suggests this is a known gap in `sync.Cond` but `context.AfterFunc` avoids
the need for an external dependency.

### gRPC Server

The server implements four RPCs defined in `worker.proto`:

| RPC | Type | Description |
| ----- | ------ | ------------- |
| `CreateJob` | Unary | Launches a process, returns a job ID. Caller becomes owner. |
| `CancelJob` | Unary | Kills the job's process. Owner or admin only. |
| `GetStatus` | Unary | Returns lifecycle state, exit code, and owner CN. |
| `WatchJobOutput` | Server-streaming | Replays output from start, then follows live. |

The server is a thin translation layer between gRPC types and the worker library.
Domain errors (e.g., `ErrJobNotFound`) are mapped to gRPC status codes
(`codes.NotFound`). Internal errors are wrapped with `codes.Internal` to avoid
leaking OS-level details to clients.

Authorization is enforced per-request. A gRPC interceptor extracts the caller's
identity (CN and OU) from the verified peer certificate and attaches it to the
request context. Handlers that operate on a specific job then check that the
caller is the job owner or an admin before proceeding.

### CLI Client

The CLI (`jobctl`) will be built with [spf13/cobra](https://github.com/spf13/cobra)
and provide subcommands that map 1:1 to the gRPC RPCs. All commands will require a
`--server` flag (defaulting to `localhost:50055`), client certificate and key paths
(`--cert`, `--key`), and a CA certificate path (`--ca`) for mTLS.

### UX

```bash
# Alice starts a job (her cert CN is "alice")
$ jobctl --cert alice.crt --key alice.key \
    start -- /usr/bin/find / -name "*.log" -mtime -7
Job created: a1b2c3d4-e5f6-7890-abcd-ef1234567890

# Alice checks status — she is the owner, access granted
$ jobctl --cert alice.crt --key alice.key \
    status a1b2c3d4-e5f6-7890-abcd-ef1234567890
Job:    a1b2c3d4-e5f6-7890-abcd-ef1234567890
Owner:  alice
State:  RUNNING

# Alice watches output
$ jobctl --cert alice.crt --key alice.key \
    watch a1b2c3d4-e5f6-7890-abcd-ef1234567890
/var/log/syslog
/var/log/auth.log
...
^C

# Bob tries to view Alice's job — denied (bob is not the owner or an admin)
$ jobctl --cert bob.crt --key bob.key \
    status a1b2c3d4-e5f6-7890-abcd-ef1234567890
Error: permission denied: only the job owner or an admin can perform this action

# An admin can access and cancel any job regardless of ownership
$ jobctl --cert admin.crt --key admin.key \
    cancel a1b2c3d4-e5f6-7890-abcd-ef1234567890
Job cancelled: a1b2c3d4-e5f6-7890-abcd-ef1234567890

# Error cases
$ jobctl --cert alice.crt --key alice.key cancel nonexistent-id
Error: job nonexistent-id not found

$ jobctl --cert alice.crt --key alice.key start
Error: argv must not be empty
```

### Security

**Transport:** TLS 1.3 only (`tls.Config.MinVersion = tls.VersionTLS13`). TLS 1.3
has a fixed set of strong cipher suites.  With TLS 1.3 there is no configuration
needed and weak options can not be selected.

**Authentication:** Mutual TLS. The server is configured with
`ClientAuth: tls.RequireAndVerifyClientCert` and a CA certificate pool. Both
client and server present certificates signed by a shared CA. Identity is
established cryptographically before application data can be exchanged.
Certificates will include appropriate Extended Key Usage (EKU) constraints
(e.g., ClientAuth for clients and ServerAuth for servers) to limit their
intended use. Enforcement of these constraints will occur during TLS
verification. All certificates will use RSA 4096-bit keys. ECDSA (P-256) could
be a lighter-weight alternative with smaller keys and faster handshakes, but
I think RSA 4096 is a straightforward and widely supported choice.

**Authorization** uses two fields from the client's X.509 certificate to make
access decisions:

- **Common Name (CN)** - the user's unique identity (e.g., `alice`, `bob`,
  `deploy-bot`). This is the string recorded as the job owner. Must be
  non-empty; connections with an empty CN are rejected.
- **Organizational Unit (OU)** - the user's privilege class. The only privileged
  value is `admin`. Any other OU (or no OU at all) is treated as a regular user.

The CA creates each client certificate with these fields set at signing time. They
cannot be changed by the client at runtime because they are cryptographically bound to
the certificate.

| Certificate file | CN | OU | Meaning |
| ------------------ | ---- | ---- | --------- |
| `alice.crt` | `alice` | `eng` | Regular user "alice" |
| `bob.crt` | `bob` | `eng` | Regular user "bob" |
| `deploy-bot.crt` | `deploy-bot` | `ci` | Regular user (service account) |
| `admin.crt` | `admin-ops` | `admin` | Admin — full access to all jobs |

There can be an unlimited number of client certificates. Every certificate signed
by the trusted CA is a valid identity. The server does not maintain a user
registry — identity comes entirely from the certificate presented during the mTLS
handshake.

## Trade-offs

- **In-memory state** — All job state and output live in process memory. A server
  restart loses everything. For a prototype this is acceptable per the
  challenge guidance on cutting scope; a production system would persist state
  to disk or at least checkpoint output to a file.

- **Pre-generated certificates** — I may plan to commit pre-generated test
  certificates and include a script to regenerate them. A production deployment
  would use automated certificate issuance and rotation
  ([cfssl](https://github.com/cloudflare/cfssl) or a Vault PKI backend).

- **TLS 1.3 only** — Both client and server are Go binaries I control, so
  there is no need for TLS 1.2 fallback. This simplifies configuration since
  TLS 1.3 eliminates cipher suite negotiation.

- **Unbounded output buffer** — Long-running jobs with large output could consume
  memory without limit. A production system would cap buffer size or write to
  disk or database. This could be captured as future enhancements.

## Edge Cases

- **Executable not found** — `exec.Command.Start()` returns an error. The gRPC
  response returns `INVALID_ARGUMENT` with the executable name so the caller
  knows what failed. Other internal errors (path lookup, permission, etc) can
  use `INTERNAL` with a generic message to avoid leaking OS-level details.

- **Cancel on a finished job** — `CancelJob` is idempotent. Calling it on a
  `Completed` or `Failed` job returns success.

- **Client disconnects mid-stream** — The gRPC stream context is cancelled. The
  watcher goroutine calls `Broadcast()` to unblock the reader, which checks
  `ctx.Err()` and exits cleanly. No goroutine leak.

- **CLI Validation** — usability-focused validation (required parameters,
  basic argument sanity checks).

- **gRPC Validation** — trust-boundary validation of incoming requests
  (required fields, allowed values, safety constraints; empty
  argv is rejected with `INVALID_ARGUMENT` before reaching the worker
  library; may evolve to include sanitization and policy enforcement).

- **Core Library** — invariant and consistency-focused validation (
  enforcing valid state transitions and internal correctness guarantees).
