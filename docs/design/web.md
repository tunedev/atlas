# The web UI

`atlas -serve` runs a local web UI over the same runner, registry and store the CLI uses. The
UI is generic: a pack ships a view file that declares its screens, and every screen and every
action is a pack run through `app.Runner`. The Svelte app and the Go adapter know no pack's
vocabulary. Nothing under `internal/core/` knows the web surface exists.

The adapter is `internal/adapters/inbound/web`. The contract is `proto/atlas/web/v1/ui.proto`,
served by connect-go, so the browser speaks Connect JSON over HTTP/1.1 and any gRPC client can
call the same methods.

## Starting it

```
atlas -serve -web-views packs/job-hunt.ui.yaml
```

It prints two lines to stderr, then serves until Ctrl-C:

```
atlas: serving http://127.0.0.1:7878/#token=<64 hex characters>
atlas: the CLI cannot use this store while the server runs; stop it with Ctrl-C
```

Open the first URL. The token is fresh on every start and is printed once. Ctrl-C cancels any
open run and any pending permission ask (which denies), then exits 0.

| Setting | Flag | Environment | Default |
|---|---|---|---|
| Serve instead of running a pack | `-serve` | | off |
| Listen address, loopback IP only | `-web-addr` | `ATLAS_WEB_ADDR` | `127.0.0.1:7878` |
| View files, comma-separated | `-web-views` | `ATLAS_WEB_VIEWS` | none; required |
| Bound on one run | `-web-run-timeout` | `ATLAS_WEB_RUN_TIMEOUT` | `10m` |
| Bound on one permission ask | `-web-ask-timeout` | `ATLAS_WEB_ASK_TIMEOUT` | `5m` |
| Bound on reading request headers | `-web-header-timeout` | `ATLAS_WEB_HEADER_TIMEOUT` | `10s` |
| Root that `file` downloads are served from | `-web-files-root` | `ATLAS_WEB_FILES_ROOT` | empty: file downloads off |

Config validation refuses `-serve` with `-pack`, `-serve` with no view file, an address whose
host is not a loopback IP literal (`localhost` included), and a non-positive timeout. There is
no way to bind beyond loopback.

`-serve` holds the store lock for its whole life, so a CLI run on the same store is refused
until the server stops.

## Remote use

Tunnel the port over SSH, keeping the same port number on both ends:

```
ssh -L 7878:127.0.0.1:7878 host
```

Then open the printed URL on the local machine. The server checks `Host` and `Origin` against
the exact address it is bound to, so the browser must use `127.0.0.1:7878`: a different local
port, or `localhost`, is refused with 421 or 403.

## The request path

A browser request passes through five stages, in order.

**1. Guards** (`guard.go`), on every request, before any handler:

- Every response carries `Content-Security-Policy: default-src 'self'; frame-ancestors 'none'`,
  `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`. No `Access-Control-*`
  header is ever set.
- `Host` must equal the bound `ip:port`, or the answer is 421. This stops DNS rebinding.
- Any method other than GET and HEAD must carry `Origin: http://<bound ip:port>`, or the answer
  is 403. A missing `Origin` is refused.
- `POST /session` exchanges the startup token for the `atlas_session` cookie (`HttpOnly`,
  `SameSite=Strict`, `Path=/`). The comparison is constant-time.
- Every RPC path needs that cookie, compared in constant time, or it gets Connect's
  `unauthenticated`.

Static files (the embedded Svelte build, `//go:embed dist`) need no session: they carry no
data. The page reads the token from the URL fragment, which the browser never sends, posts it
to `/session`, and removes it from the address bar.

**2. RPC.** `UIService` has five methods:

| RPC | Shape | Does |
|---|---|---|
| `Views` | unary | The loaded views as JSON, the egress table with each hosted endpoint's acknowledgement, and whether file downloads are on |
| `Run` | server stream | Runs one declared screen or action. Streams `Queued`, then a `Step` per step event, any `Ask`, and ends with `Done` carrying the state as JSON. Or sends one `NeedsAcknowledgement` and stops. |
| `Answer` | unary | Allows or denies a pending ask by id |
| `Acknowledge` | unary | Records consent for one endpoint of the egress table |
| `Document` | unary | Returns one record document or one file under the files root, for download |

Protobuf types stop at the adapter. `web` passes domain types to the runner.

**3. View resolution** (`run.go`, `bind.go`). A `RunRequest` names a view id, a screen id, an
action index (`-1` for the screen itself), params and inputs. It never names a pack, a tool or
a var. The server:

- finds the screen, and the action if one is named;
- refuses a param the screen does not declare and an input the action does not declare;
- refuses an input value outside the action's declared `options`;
- resolves every var from its binding (below). An action that binds `state.` reads the screen's
  last result from the server-side cache, keyed by view, screen and params; if the screen has
  not run with those params, the action is refused with "reload the screen";
- loads the pack the view names, relative to the view file, and applies the vars.

**4. The consent gate, then the run slot.** While any hosted endpoint is unacknowledged, `Run`
sends `NeedsAcknowledgement` and runs nothing (see Consent). Otherwise the run takes the single
run slot: one run executes at a time in the process, because `gitdocs` has no locking. A run
first receives `Queued{ahead}`, the number of runs waiting for or holding the slot in front of
it, then waits. Internal reads the server makes (acknowledgements, record documents) take the
same slot.

**5. `Runner`.** The run executes on the shared runner through a per-run `WithProgress` copy,
bounded by the run timeout. Each `StepEvent` becomes a `Step` message. While the run holds the
slot, the web asker sends its permission asks on this run's stream. A screen's final state is
cached for its actions; an action's is not, and the browser re-runs the screen after an action
succeeds.

## View files

A view file is YAML named `<id>.ui.yaml`. Its id is the file name without `.ui.yaml`, and
paths inside it resolve relative to the file. Decoding is strict: an unknown top-level,
screen, action or input key fails.

```yaml
title: Logbook
discloses:            # what these packs may send to a model endpoint
  - entry text
screens:
  - id: days
    title: Days
    run: days.yaml
    show:
      - table: {from: rows, columns: [day, mood], open: {screen: day, param: {day: day}}}
  - id: day
    title: Day
    params: [day]
    run: day.yaml
    vars: {day: param.day}
    show:
      - fields: {from: row, keys: [day, mood]}
    actions:
      - label: Save
        run: note.yaml
        input:
          - {name: text, text: true}
          - {name: mood, options: [calm, rough]}
        vars: {day: param.day, text: input.text, mood: input.mood, path: state.row.path}
```

| Key | Where | Meaning |
|---|---|---|
| `title` | view, screen | Shown in the UI |
| `discloses` | view | Plain-language classes of data these packs may put in a prompt. The consent sentence shows the union across every loaded view. |
| `id` | screen | Unique in the view; the URL is `#/<view>/<screen>?<param>=<value>` |
| `params` | screen | Names the URL may carry |
| `run`, `vars` | screen, action | The pack to run and where each of its vars comes from. A screen with no `run` cannot be run; opening it shows that error. |
| `show` | screen | Widgets over the screen's last state |
| `actions` | screen | Buttons. `label`, `run`, `vars`, and `input` fields: `options` for a closed choice, `text: true` for free text |

### Widgets

Every widget reads a dotted path (`from`) into the screen's state, where a numeric segment
indexes a list. Everything renders as text; the web source is scanned for `{@html}` and fails
the build if it appears.

| Widget | Renders | Extra keys |
|---|---|---|
| `table` | One row per list element, one cell per column (each column is a dotted path into the row). With `open`, the first cell links to another screen, filling its params from row paths. | `columns`, `open: {screen, param: {<param>: <row path>}}` |
| `fields` | Label and value pairs of an object | `keys` |
| `badge` | One short value | |
| `list` | A list, one item per line | |
| `text` | Long text, preformatted | |
| `bins` | A calibration report's bins: the `reads` sentence, n, and bars for predicted and observed. A null `observed_rate` shows "too few to say (n=…)". | |
| `document` | A download button for a record path, through `Document` | |
| `file` | A download button for a path under the files root, through `Document` | |

### Binding sources

A var's value comes only from one of these. Anything else fails at load.

| Source | Value | Allowed in |
|---|---|---|
| `param.<name>` | A param from the URL; must be declared by the screen | screens, actions |
| `input.<name>` | A form value; must be declared by the action | actions |
| `state.<path>` | The screen's cached last state at a dotted path. A string is used as is; anything else is JSON-encoded. | actions |
| `const.<value>` | The literal after `const.` | screens, actions |
| `server.<name>` | `store_root` or `files_root`, from config | screens, actions |

### Validated at boot

`LoadViews` fails `-serve` at startup, naming the file and screen, when:

- a view id or a screen id is repeated;
- a widget kind is unknown, or an `open` names a missing screen or a param that screen lacks;
- a `run` does not load as a pack;
- a var is not declared by the pack it binds;
- a binding uses an unknown source, `input.` or `state.` in a screen's own vars, an undeclared
  param or input, or a `server.` name other than the two.

A child pack reached through `pack.each` is loaded when it runs, not at boot.

## Reads are tools

Screens read the record through generic tools, so the server has one path to the record:
`Runner.Run`.

| Tool | With | Returns |
|---|---|---|
| `index.find` | `kind`, optional `match` (YAML map of field equalities), optional `limit` | `{rows: [{path, rev, kind, fields, when}]}`, newest first |
| `docs.get` | `path`, optional `rev` | `{path, text}`, plus `doc` when the text is JSON |
| `judge.assess` | `path` of a recorded judgement, `verdict` question id | `{verdict, reasons, tripped}`, re-assessed from the stored answers and rules |

## Consent

**The egress table.** `cmd/atlas` builds one row per endpoint the registry sends data to:

- the model endpoint: `Model.BaseURL` reduced to scheme and host, with the tools built over the
  model provider, judge or extractor. It is hosted unless the host is `localhost` or a loopback
  IP literal; a name is never resolved.
- the agent, when one is configured: `agent:<command>`, tool `agent.do`, always hosted.

`TestEgressTableCoversModelTools` fails when a tool built over the model clients is missing
from the table.

**The gate.** It is per server, not per run: while any hosted endpoint lacks an
acknowledgement covering every class the loaded views disclose, every `Run` answers
`NeedsAcknowledgement{endpoints, discloses}` and runs nothing. The browser shows:

> This will send **<classes>** to **<endpoints>**, which is not on this machine. Nothing is
> sent until you agree. [Send and remember for this endpoint] [Cancel]

**The record.** `Acknowledge` runs a one-step `docs.put` blueprint through the runner:
`atlas/egress/<first 16 hex of sha256(endpoint)>.json`, kind `egress`, holding the endpoint,
the disclosed classes and the time, and committed like any other write. An acknowledgement
counts only if it covers every class currently disclosed, so a changed `Model.BaseURL` or a
new class in `discloses` asks again. A local engine never asks.

**Memory.** The first `Views` or `Run` reads every hosted endpoint's acknowledgements with
`index.find`, taking the run slot once. From then on the gate and `Views` answer from memory,
and `Acknowledge` updates it. The store lock keeps other processes from writing the record
while the server runs.

**The badge.** The header of every screen reads "Data leaves this machine: <endpoints>" while
any endpoint is hosted, and "Everything stays on this machine" otherwise. Clicking it shows the
egress table.

## Permission asks

With `-serve`, the agent's human permission is the web asker, in place of the terminal prompt,
behind the same `app.PermissionPolicy` rules. An ask goes as an `Ask` message on the stream of
the run holding the slot and waits for `Answer`. It denies when no run is active, when the
stream closes, when the run ends, when the ask timeout passes, or when the answer is deny.

## Documents

`Document` returns bytes for the browser to save through a Blob and `<a download>`; nothing is
rendered inline.

- `record`: reads the path through a one-step `docs.get` blueprint. Media type is
  `application/json` when the text parses as JSON, `text/plain` otherwise.
- `file`: reads the path under `Web.FilesRoot` through `os.Root`, which refuses `..`, absolute
  paths and symlinks that escape the root. Only regular files up to 32 MiB. Refused when no
  files root is configured.

## What is never exposed

| Never | How |
|---|---|
| Reachable from another machine | Loopback-only bind, validated at boot, with no override |
| Reachable from another local user or process without the token | 32 random bytes per start, printed once; the session cookie is checked on every RPC |
| Readable or writable from a web page the user visits | Host check, Origin check on every write, no CORS headers, `frame-ancestors 'none'` |
| A pack, tool, var or filesystem path chosen by the browser | Closed binding sources; `Run` takes ids and declared values only; files only under the files root |
| The model API key | The web package never receives config, and the egress table carries scheme and host only. A model error body that echoes the key has it replaced by `[redacted]` in the provider adapter, so no surface (Run stream, CLI stderr, log, commit) sees it. |
| Data sent to a hosted endpoint without the user agreeing | The consent gate |
| Script from the record running in the UI's origin | Text-only rendering, no `{@html}`, downloads as attachments, CSP `default-src 'self'` |

## Regenerating

The generated Go (`internal/adapters/inbound/web/uiv1`), the generated TypeScript
(`web/src/gen`) and the built bundle (`internal/adapters/inbound/web/dist`) are committed, so
`go build` needs neither `buf` nor Node.

After changing `ui.proto`:

```
cd web && npm ci && cd ..   # protoc-gen-es comes from web/node_modules
buf generate
```

After changing anything under `web/`:

```
cd web && npm run build
```

Commit the results. CI rebuilds the bundle and fails when `dist/` differs from what is
committed. Connect code is generated into `uiv1` itself (`package_suffix` with an empty value),
because a `uiv1connect` subpackage would import its parent, which the inbound isolation rule
forbids.

## Limits

- One run at a time. A tool that ignores its context holds the run slot until it returns.
- Progress is per step. A long `judge.each` is one step, and a `pack.each` child's steps print
  to the server's stderr rather than the browser.
- An action binds state by a fixed path, so it acts on one row (`rows.0`), never a row the user
  picks.
- The session cookie is scoped by host, not port. A listener on another loopback port that the
  browser visits receives it, and its value is the startup token, which grants a session here.
