# Download queue

Every download goes through a queue in the database. Whoever wants a file
downloaded (the web UI, the CLI, a scheduled sync, enabling auto-download)
adds a job; workers take jobs from the queue and run them. This makes the
server the owner of downloads while it runs, retries transient failures, and
keeps an honest record of files that were given up.

## Jobs

Table `download_jobs`, one row per file that is waiting for or in a download:

| Column | Meaning |
|--------|---------|
| `file_id` | primary key |
| `state` | `queued`, `running`, `retrying` or `failed` |
| `attempts`, `max_attempts` | attempts made so far and the limit (`BULK_LOADER_MAX_ATTEMPTS`, default 5) |
| `next_attempt_at` | when a `retrying` job is due |
| `last_error` | error of the last attempt |
| `requested_by` | who queued the file first: `api` (web UI and API), `schedule`, `auto` (enabling auto-download) or `cli` |
| `claimed_by`, `lease_until` | the process running the job (`host:pid`) and until when its claim holds |
| `cancel_requested` | set when another process asks for the running download to stop |
| `version` | optimistic lock for claiming |

A finished download deletes its job; `download_entries` stays the history of
attempts. A cancelled download deletes its job too. A `failed` job stays until
the file is requested again, which resets it to `queued`. Queuing a file that
already has a job changes nothing, so the first requester stays recorded.
Turning auto-download off removes the product's waiting `schedule` and `auto`
jobs; files queued by hand stay queued.

## Cancelling

A waiting job (`queued`, `retrying`, `failed`) is deleted directly. For a
running job the process that runs it stops the download itself:

- in the same process, the download is cancelled at once;
- in another process, `cancel_requested` is set; the worker sees it at its
  next lease renewal (within 30 seconds), stops the download and deletes the
  job;
- a running job whose lease has expired (its process died) is deleted
  directly.

Times in the queue are stored in UTC.

## Workers

The server runs `BULK_LOADER_MAX_CONCURRENT` workers. A worker claims a due
job (`queued`, `retrying` past `next_attempt_at`, or `running` with an expired
lease) with a conditional update on `version`, so two workers cannot claim
the same job at once. While it downloads it renews the lease every 30 seconds;
a lease lasts two minutes, so a job of a process that died is taken over by
another worker after at most two minutes.

A worker whose renewals fail for most of the lease, or that finds the job
claimed by someone else, stops its download and leaves the job alone: from
then on another worker may own it. Leases compare wall clocks, so processes
on different hosts sharing a database need synchronized clocks (NTP).

Outcome of an attempt:

- success: the job is deleted;
- cancelled by the user: the job is deleted;
- the worker shuts down: the job goes back to `queued` and the attempt does
  not count;
- the worker lost the job to another worker: the job is left to that worker;
- the file is being downloaded outside the queue right now: the job is due
  again shortly and the attempt does not count;
- permanent error (authentication, not found, invalid configuration): `failed`;
- transient error (network, rate limit, timeout, anything else): `retrying`
  after 1, 2, 4, 8 ... minutes (capped at one hour, with 20 % jitter), and
  `failed` once `max_attempts` attempts were made.

## Server and CLI

`serve` writes a heartbeat (`server_heartbeat` setting: time, process, version,
start time) every 10 seconds. A CLI command that downloads (`download all`,
`file download`, `pull`) adds its files to the queue and keeps their IDs:

- when a server's heartbeat is fresh (under 30 seconds old), the command
  returns and the server downloads; `--wait` follows the queue until its
  files are done, and takes over with its own workers if the server stops;
- otherwise the command runs its own workers over its files and exits when
  every one is downloaded or given up. Files another process is downloading
  are waited for, not downloaded twice.

The exit code reflects the end state: 0 when all files were downloaded, 3 when
some were given up, 1 when all were. `status` reports the server's heartbeat,
the queue and the transfers in progress from any process.

The server also reloads product schedules every 30 seconds, so a schedule
changed with the CLI takes effect without a restart.

On startup a process marks downloads left `downloading` by a dead process as
interrupted, except those whose job another process holds a valid lease on.
