# Where regtool's mirror list comes from

regtool switches package managers between registry mirrors it reads out of a
single JSON document: a map of region to package manager to URLs. The document
is maintained remotely so a mirror that moves can be fixed without shipping a
new binary, and there is a local cache and a compiled-in copy behind it so the
tool still works on a laptop with no network.

`regtool sources` prints which of them this machine is actually using.

## Resolution order

1. **`REGTOOL_SOURCES_FILE`** — when it names a file, that file is the mirror
   list and nothing else is consulted. Failing to read it is an error, because
   a caller that named a file meant that file.
2. **The sources URL** — `REGTOOL_SOURCES_URL`, or the built-in default when it
   is unset. The request carries `If-None-Match` with the cached ETag when there
   is a cache, so an unchanged document costs a `304` and no body. A `200` is
   parsed, checked for at least one region carrying at least one registry, and
   written to the cache together with its new ETag. The client gives up after
   ten seconds.
3. **The cache** — used when the server answered `304`, and as the fallback when
   the fetch failed for any reason at all: no route, a refusal, a non-2xx, a
   body that is not JSON, a document with nothing usable in it. The fallback is
   logged at warn level with the reason.
4. **The embedded copy** — `source/sources.json`, compiled into the binary. It
   is what is left when there is no cache either.

`REGTOOL_OFFLINE=1` skips step 2 entirely: the cache is used when it exists and
the embedded copy otherwise. Nothing in the chain below the sources file is ever
fatal, so a load only fails when a named file cannot be read.

## Environment variables

| Variable | What it does |
| --- | --- |
| `REGTOOL_SOURCES_URL` | The sources document to fetch. Anything serving the same JSON shape works, including a self-hosted service. |
| `REGTOOL_SOURCES_FILE` | A sources file on disk to use instead of everything else. |
| `REGTOOL_OFFLINE` | Any non-empty value skips the network. |
| `REGTOOL_CONFIG_DIR` | The directory regtool keeps its own files in. Defaults to `<os.UserConfigDir>/regtool`. |

## The cache file

`<config dir>/sources.cache.json`, written atomically through a temporary file
in the same directory followed by a rename, so a reader never sees half of an
update. It is one file: the document and the ETag that labels it can never drift
apart.

```json
{
  "url": "https://example.com/v1/sources",
  "etag": "\"6f1a...\"",
  "fetched_at": "2026-09-14T08:31:04.913Z",
  "sources": {
    "cn": { "npm": ["https://registry.npmmirror.com"] },
    "us": { "npm": ["https://registry.npmjs.org"] }
  }
}
```

`url` is what makes the cache safe to keep across a change of sources URL: a
cache written for a different URL is ignored rather than served, and the next
fetch is unconditional.

## `regtool sources`

```
$ regtool sources
url      https://example.com/v1/sources
origin   cache
offline  no
cache    /home/you/.config/regtool/sources.cache.json
etag     "6f1a9c3e"
fetched  2026-09-14T08:31:04+02:00
regions  3
apps     11
```

`origin` is one of `file`, `remote`, `cache` or `embedded`: which step of the
chain above answered. `etag` and `fetched` are `none` when there is no cache for
the URL in use. `--json` prints the same fields, with `fetchedAt` as a timestamp
and `etag` left out when there is nothing cached.

### `regtool sources refresh`

Fetches the document again without asking the server whether the cached copy is
still current, rewrites the cache with what came back and reports whether
anything changed:

```
$ regtool sources refresh
url      https://example.com/v1/sources
cache    /home/you/.config/regtool/sources.cache.json
etag     "8b27d104"
fetched  2026-09-14T09:02:55+02:00
changed  yes
regions  3
apps     11
```

Unlike every other command it does not fall back: a fetch that fails is an
error, because quietly keeping the cache is exactly what refresh was asked not
to do. It also refuses to run under `REGTOOL_OFFLINE`, or when
`REGTOOL_SOURCES_FILE` means there is no remote document in play.

## Serving the document yourself

Point `REGTOOL_SOURCES_URL` at anything that returns the same JSON shape as
`source/sources.json`. Sending a strong `ETag` and honouring `If-None-Match`
turns every run after the first into a `304`; `Cache-Control: public,
max-age=300` is what the reference service sends.
