# drupaltui

[![CI](https://github.com/joehahndev/drupaltui/actions/workflows/ci.yml/badge.svg)](https://github.com/joehahndev/drupaltui/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/joehahndev/drupaltui)](https://github.com/joehahndev/drupaltui/releases)

A terminal UI for writing content on a Drupal site over **JSON:API + OAuth2**
(client credentials grant via the `simple_oauth` module).

It supports the common content-writing workflow — browse, create, edit in markdown,
publish, unpublish — and deliberately supports **no deleting or destructive actions**:

- The HTTP client can only issue `GET`, `POST`, and `PATCH`. There is no delete code path.
- A content type can only be **written to** if it has **"Create new revision"** enabled,
  so every change made through the tool is recoverable on the server. Content types
  without revisions (or whose settings can't be verified) are browsable but read-only.
- New content is always created **unpublished**; publishing is a separate, confirmed action.

## Install

Prebuilt binaries for Linux, macOS and Windows (amd64 and arm64) are on the
[releases page](https://github.com/joehahndev/drupaltui/releases). Or, with a
Go toolchain:

```
go install github.com/joehahndev/drupaltui/cmd/drupaltui@latest
```

To build from a checkout:

```
go build ./cmd/drupaltui
```

## Drupal site prerequisites

1. Enable the **`jsonapi`** core module and the
   [**`simple_oauth`**](https://www.drupal.org/project/simple_oauth) module, plus a
   markdown text format module
   (e.g. [`markdown_easy`](https://www.drupal.org/project/markdown_easy)).
2. Generate the OAuth keys at **`/admin/config/people/simple_oauth`** if you
   haven't already.
3. Create a consumer at **`/admin/config/services/consumers/add`**:
   - Grant type: **Client credentials**
   - Confidential: yes; note the **client ID** and set a **client secret**
   - Assign a **user** to the consumer — tokens act as that account, so its
     permissions are what drupaltui gets.
4. At **`/admin/config/services/jsonapi`**, select **"Accept all JSON:API create,
   read, and update operations"**. (The default is read-only; all writes would fail
   with 405.)
5. On each content type you want to edit (`/admin/structure/types/manage/{type}`),
   check **"Create new revision"**. drupaltui refuses to write to types without it.
6. The consumer's user needs, per bundle: *access content*, *create/edit*
   permissions, permission to **use the markdown text format**, and — for
   publish/unpublish — publish permissions (or *administer nodes*). To let drupaltui
   verify revision settings, it also needs to read node type config via JSON:API
   (typically *administer content types*); without that, drupaltui runs **read-only
   by design** and says so.

## Client configuration

On first run, drupaltui prompts for the site URL, OAuth client ID, and client
secret, then lets you pick the text format for markdown bodies. URL, client ID,
and format are saved to:

- Windows: `%APPDATA%\drupaltui\config.yml`
- Linux/macOS: `~/.config/drupaltui/config.yml`

The **client secret is never written to disk**. Provide it via the
`DRUPALTUI_CLIENT_SECRET` environment variable to skip the prompt, or type it at
startup. Access tokens are held in memory, cached until expiry, and refreshed
automatically (including a one-shot retry if the site revokes a token mid-session).

Optional environment: `VISUAL` or `EDITOR` selects the editor used for markdown
bodies (values like `code --wait` work). Defaults to `notepad` on Windows.

Flags: `-config <path>` alternate config file, `-url <base>` override site URL,
`-version`.

## Keys

| Screen | Keys |
|---|---|
| Content types | `enter` open · `/` fuzzy filter · `q` quit |
| Content list | `enter` view · `e` edit · `n` new · `p` publish/unpublish (confirms) · `/` server-side title filter · `m` more pages · `r` refresh · `esc` back |
| Detail | `e` edit · `p` publish/unpublish · arrows scroll · `esc` back |
| Edit | title in-TUI → body in your `$EDITOR` → confirm screen: `y` save · `e` re-edit body · `t` edit title · `l` revision log message · `esc` discard (confirms) |
| Anywhere | `ctrl+c` quit |

Every save writes an (editable) revision log message, defaulting to
"Created/Edited via drupaltui", so the site's revision history shows what happened.

## Development

```
go test ./...    # unit tests: API client (httptest fixtures), config, editor, UI gates
go vet ./...
```

The test suite includes guards that fail if any code path ever issues a DELETE
request, and that the revision gate blocks writes at both the keybinding layer and
the request layer.
