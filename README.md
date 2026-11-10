# logos-dumbo

Dumbo for Logos, the channel and the factory in one repo and one image (as
`logos-ssh`): the factory builds **PHP pages**, `bind-call` minions that only
answer, and the channel calls a page's URL when core has something for it.
The elephant is PHP's.

A proof of concept for `bind-call`: one target, eight commands, to see how
tasks and file transfers fit a minion core reaches only by calling it. The
channel only dials out: to core (registration over RabbitMQ, and the control
link) and to pages. It listens on no port.

Both register as module `dumbo`, with their own instance ids (`dumbo-local`,
`dumbo-factory-local`): core keys registrations by instance id.

## The channel

### How a call goes

1. At start the channel registers with core as `module_type=channel`,
   `module_name=dumbo`, `instance=CHANNEL_ID`, and opens its **control link**:
   a WebSocket to `LOGOS_CORE_BASE_URL` + `/api/channel/link` (channel-core's
   `linkclient`). Core refuses it (403) until the instance is registered; the
   channel keeps trying, backing off from 1s to 30s, and reopens it whenever
   it drops.
2. Core sends a **call** over the link: the build connection's and the
   target's values, the id the message is sent under, and the sealed
   message.
3. The channel sends the page one HTTP request (below) and answers core with
   what came back, by the call's request id:
   - **the page's answer**: a `200`, `application/octet-stream`, at least an
     id and the smallest sealed frame: the id and the frame go to core, which
     opens it. The channel never reads the frame.
   - **something else answered** (a 404, a WAF's page, a redirect): the status
     and the first 512 bytes, so core can show the target as *not the
     minion*.
   - **nothing answered**: a failure, `unreachable` (DNS, refused, no route),
     `timeout`, `tls` or `other`.

   An answer that reached something carries the IP address the call went
   to, which core shows as the session's external address: the one
   connected to, or through a proxy the URL's host when it is an IP address
   (a name, the proxy resolves: none).

See the docs' [Channel ↔ Core: Connections](https://logoscore.github.io/logos-docs/contracts/channel-core-connections/).

### Wire format (channel ↔ page)

Both ways, the body is:

```
[id: 16 bytes, raw UUID][sealed frame]
```

- **Request**: the connection's `method` (`POST`, `PUT` or `PATCH`; built into
  the page), `Content-Type: application/octet-stream`, to the target's URL.
- **Answer**: `200`, `application/octet-stream`, under the same id the call
  came with. A page answers anything else (another method, a body that does
  not open) with a plain 404, as a missing page would.

### Connection schema

Declared at registration (`schema_version` "2", version 1), one kind:

| Kind | Built into the page (`fields`) | Per target (`target`) |
|---|---|---|
| `bind-call` | `method` | `url` (required), `host_header`, `proxy` (http, https or socks5), `tls_verify` (off by default), `user_agent` (a common browser's when empty), `headers` |

Target values never go into a page: operators fill them in when the page is
on a server, and only the channel uses them.

### Configuration

Environment only (a `.env` file fills in what the environment does not set):

| Variable | Default | |
|---|---|---|
| `CHANNEL_ID` | `dumbo-main` | the instance id; run each instance with its own |
| `LOGOS_CORE_BASE_URL` | `http://localhost:8002` | core's base URL: the control link opens under it |
| `LOGOS_CORE_CA_FILE`, `LOGOS_CORE_CERT_SHA256`, `LOGOS_CORE_TLS_INSECURE` | none (the system's trust) | how it trusts core's certificate over HTTPS, for a self-signed one: a PEM to trust, the certificate's SHA-256 fingerprint, or no verification; see [How modules reach core](https://logoscore.github.io/logos-docs/core-infrastructure/#how-modules-reach-core) |
| `CALL_TIMEOUT` | `30s` | how long a call waits for a page, 1s to 10m (core's deadline wins when shorter) |
| `MAX_BODY_BYTES` | `1048576` | largest answer taken from a page |
| `LOG_FORMAT` | `json` | `json` or `text`, on stderr |
| `RABBITMQ_HOST`, `RABBITMQ_PORT`, `RABBITMQ_USER`, `RABBITMQ_PASSWORD`, `RABBITMQ_VHOST` | `localhost`, `5672`, `guest`, `guest`, `/` | core's RabbitMQ |

### Management

| Key | Type | Applies | |
|---|---|---|---|
| `call_timeout` | duration | live | 1s to 10m |
| `max_body_bytes` | bytes | live | |
| `log_level` | `debug` / `info` / `warn` / `error` | live | `debug` logs every call; `info` calls that failed or reached something other than a page |

Actions: `drain` (every call fails at once until resumed) and `resume`.
Status: the control link (up or down), calls, pages answered, something
else answered, calls failed, draining.

On SIGINT/SIGTERM the channel stops taking calls, lets those in progress
finish (up to `CALL_TIMEOUT` + 5s), closes the link and deregisters.

## The factory

### What it builds

| | |
|---|---|
| Target | `php-page`: one `.php` file (`dumbo-<id8>.php`), platform `any/any`, PHP 7.0 or later with openssl |
| Channel | `dumbo`, connection schema version 1, kind `bind-call` (Dumbo adds no parameters of its own) |
| Crypto | `aes-256-cbc-hmac-sha256`: core generates the build's keys, Dumbo writes them into the page |
| Fields | `greeting` (default "hello from Dumbo"): what the hello command answers |
| Commands | `hello` (op 1, required): answers with the greeting; `download` (op 2, `download_file`): a file from the server to core; `upload` (op 3, `upload_file`): a file from core onto the server; `ls` (op 4, `list_dir`): a folder's entries; `rm` (op 5, `delete_path`): a file, a link or a folder; `stream_download` (op 6, `stream_file`): a file of any size to whoever downloads it; `stream_upload` (op 7, `upload_stream`): a file of any size onto the server as it is uploaded; `walk` (op 8, `walk_dir`): a folder and those under it, into core's file browser |

The page answers the build's first `bind-call` connection through the Dumbo
channel: its `method` (from the endpoint, `POST` by default) is built in, and
its hello says that connection's place.

### How a page answers

The Dumbo channel calls the page with the connection's method and a body of
the id the message is sent under (16 bytes) and the sealed message; the page
answers `200`, `application/octet-stream`, the same id and its own sealed
message, which answers core's (`re`). Anything else (another method, a body
that does not open with the build's keys) gets a plain 404, as a missing page
would.

- **It keeps nothing between requests.** It skips staging, so its session
  keeps the build's keys, and it takes any id its keys open a message under:
  the build id at the first call, the session id after.
- **`rehello`** (core's first call, under the build id): it answers with its
  `hello`: pid, OS and architecture with Go's names, host, user, process
  (`apache2`, `php-fpm`, …), integrity (root or user, with posix), the
  server's address, the machine id (`/etc/machine-id`, else the host name,
  else a random one), its version (`dumbo-page/<factory version>`), the
  connection's place, and under `x` the PHP version, the SAPI, the server
  software and the script's path.
- **Tasks run within the call that brings them**: acknowledged and answered
  with their result at once. `hello` answers with the greeting; a command
  the page does not have fails, saying so.
- **Files move a chunk a call** (512 KiB). The page keeps nothing between
  calls: it puts what it needs to go on (the file's path, a download's
  size; an upload's temporary file and expected SHA-256) in the transfer's
  `ctx`, which core echoes on every ack and chunk it sends for the
  transfer.
  - `download`: the first chunk comes in the call that brings the task; each
    of core's acks brings the next, from where it says; the ack for the
    last ends the task with the `file_download` data (path, size, SHA-256).
  - `upload`: the page checks where the file goes (a folder takes it under
    its own name; an existing file only with `overwrite`), creates
    `.<name>.<xfer>.part` next to it and asks for the first chunk; each
    chunk is written in order (one sent twice, once) and the next asked
    for; once whole, the SHA-256 is checked, the file moved into place, and
    the task ended with the `file_upload` data. A stream (`upload_stream`)
    is refused.
  - `stream_download`: the page announces the file (a first chunk without
    data) and holds; core calls it when a downloader comes, and again each
    time the downloader takes a chunk and leaves room, its ack saying where
    from; the ack saying the downloader has every byte ends the task with
    the `file_stream` data.
  - `stream_upload`: as `upload`, the size and SHA-256 not known until the
    chunks say them; asked with no bytes there yet, core holds the page,
    and calls it when the uploader's bytes come (the page then asks); the
    last chunk's SHA-256 is checked.
  - Holds and unasked acks carry the stream's ctx too: core keeps it.
  - A task stopped mid-transfer leaves an upload's temporary file: the page
    hears no more of it, and knows no path to clean up.
- **`ls`** lists a folder (a relative path from the page's own folder, `.`
  by default): every entry with its type, size, time, mode, owner and group
  (with posix), a link's target, and dot-names hidden; in pieces of 500
  entries, at most 5000 (marked partial past that). A folder it may not
  read is a listing saying so, and the task failed.
- **`walk`** goes depth first, folders in name order, never through a link;
  `depth` (5), `max_folders` (10 000), `hidden` and `one_fs` say how far and
  where. Each call lists folders while its answer stays within 600 KB and
  10 seconds (a folder that would go past waits for the next call), and ends
  with a part saying where it stands (`ctx`: the last folder listed, the
  counts and the params; protocol v0.25.0); core answers with a `resume`
  giving it back, and the next call goes on from the folder after it. Every
  part says the walk's progress (`dir_walk`); a `cancel` (with the ctx) ends
  it where it stood.
- **`rm`** removes a file, a link (never what it points to) or a folder, a
  folder with something in it only with `recursive`; never a filesystem's
  root. The output says what went.
- **A `cancel`** gets `unknown_task`: its task already ended, or (a
  transfer) the page cannot tell. An item a page
  does not take gets `unknown_item`.
- **Every answer takes up to 4 tasks** (`get_tasks`), so the next call core
  makes may bring that many.
- The CBOR, the AES-256-CBC and the HMAC-SHA256 are the page's own code
  (openssl and hash), no libraries.

### Configuration

Environment only: `FACTORY_ID` (default `dumbo-factory-local`; core keys registrations by instance id, so it must differ from the Dumbo channel's `CHANNEL_ID`), `LOGOS_CORE_BASE_URL` (the channel's, with the same TLS settings:
builds upload their page to core), `LOG_FORMAT` (`json`
or `text`), and core's RabbitMQ: `RABBITMQ_HOST`, `RABBITMQ_PORT`,
`RABBITMQ_USER`, `RABBITMQ_PASSWORD`, `RABBITMQ_VHOST`.

Management: `log_level` (live), beside factory-core's builds section
(`max_concurrent_builds`, drain and resume, build counters).

## Image

`ghcr.io/logoscore/logos-dumbo`, published on each `v*` tag, holds both
programs: `/app/dumbo-channel` (the entrypoint) and `/app/dumbo-factory`
(run the factory with `entrypoint: ["/app/dumbo-factory"]`). It replaces the
two images `logos-dumbo-channel` and `logos-dumbo-factory`.

## Local development

`docker compose up -d --build` joins logos-core's network (start core first;
set `RABBITMQ_USER` and `RABBITMQ_PASSWORD` in `.env`): the channel, the
factory, and `dumbo-www`, a `php:8.3-apache` server for trying pages. Build a
page in core, put it in `./www`, and add a target with the URL
`http://dumbo-www/<page>.php`. From the host it is on port 8095.

## Tests

`go test ./...`. The page tests run the generated page under PHP's built-in
server and speak to it as core does (rehello, tasks, cancels, the 404s);
`TestChannelCallsThePage` puts the two halves together, the channel's caller
calling a built page. They need `php` on `PATH` and skip without it (CI sets
`DUMBO_REQUIRE_PHP=1` so they fail instead). Without a local PHP, a wrapper
that runs `docker run --rm -i --network host -v "$TMPDIR:$TMPDIR" php:8.3-cli
php "$@"` runs most of them (the relative-path `rm` test needs a real one).
