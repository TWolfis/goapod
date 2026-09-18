# GoApod

Golang library for fetching NASA's Astronomy Picture of the Day (APOD) from
the APOD Basic JSON API on science.nasa.gov:
[science.nasa.gov/wp-json/wp/v2/apod-basic](https://science.nasa.gov/wp-json/wp/v2/apod-basic).
This API replaced the retired `api.nasa.gov/planetary/apod` service; it needs
no API key.

Fetch a single date, a date range, or a batch of random dates. The API
returns at most `goapod.MaxPerPage` (25) results per request, so larger date
ranges are paged through automatically, or fetched concurrently in batches.
The repo also includes `afetch`, a CLI built on top of the library.

## Installation

```sh
go get github.com/TWolfis/goapod
```

## Library usage

Every request starts from an `*Apod`, then dispatches to the matching `Fetch`
method. Every `Fetch` takes a `context.Context` for cancellation and deadlines.
`ApodDate`, `ApodDateRange`, and `ApodCount` are mutually exclusive —
set only one. `goapod.New()` returns an `*Apod` with a default HTTP client and
base URL and everything else left at its zero value, ready to fill in:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/TWolfis/goapod"
)

func main() {
    a := goapod.New()

    if err := a.Date.Set("2023-07-20"); err != nil { // or the legacy code "230720"
        log.Fatal(err)
    }

    responses, err := a.Date.Fetch(context.Background(), a)
    if err != nil {
        log.Fatal(err)
    }

    for _, r := range responses {
        fmt.Println(r.String())
    }
}
```

Fetching a date range works the same way, just set `DateRange` instead and
call `Fetch` on it. `Fetch` walks every page of the range one after another
and returns the results newest first, as the API orders them:

```go
a := goapod.New()
a.DateRange.Set("2023-07-01, 2023-07-07") // "YYYY-MM-DD, YYYY-MM-DD"

responses, err := a.DateRange.Fetch(ctx, a)
```

Use `NewApod(date, dateRange, count)` instead of `New()` when you already
have a specific date, date range, or count and want the mutually-exclusive
combination validated up front (it panics on conflicting fields, e.g. both
`date` and `count` set).

For large ranges, `FetchinBatches` splits the range into chunks of at most
`goapod.MaximumApodRange` days (one request each) and fetches them
concurrently, streaming results back over channels:

```go
respChan, errChan := a.DateRange.FetchinBatches(context.Background(), a, 5, 25) // 5 concurrent requests, 25 days per request
```

And fetching a batch of random dates. The API has no random endpoint, so
`Count.Fetch` draws random dates itself and fetches each one (up to
`goapod.RandomConcurrency` requests at a time):

```go
a := goapod.New()
a.Count.Set("5")

responses, err := a.Count.Fetch(ctx, a)
```

A missing date comes back as an `*goapod.APIError` whose `NotFound()` method
reports true; other non-200 responses are `*APIError` values too, carrying
the WordPress error `Code` and `Message`.

### Response fields

`ApodResponse` mirrors the API's JSON:

| Field          | JSON             | Notes                                                    |
| -------------- | ---------------- | -------------------------------------------------------- |
| `Date`         | `date`           | `YYYY-MM-DD`                                             |
| `PostID`       | `post_id`        | WordPress post ID                                        |
| `Title`        | `title`          |                                                          |
| `Permalink`    | `permalink`      | APOD post URL on science.nasa.gov                        |
| `MediaType`    | `media_type`     | `image`, `video`, or `iframe`                            |
| `Explanation`  | `explanation`    | HTML fragment; use `PlainExplanation()` for plain text   |
| `Credit`       | `credit`         | HTML fragment; use `PlainCredit()` for plain text        |
| `Copyright`    | `copyright`      | HTML fragment                                            |
| `Alt`          | `alt`            | Featured image alt text                                  |
| `URL`          | `url`            | Same as `Permalink` (the post page, not the image)       |
| `Hdurl`        | `hdurl`          | Full-size featured image; a poster frame for videos      |
| `BasicHTML`    | `basic_html`     | Full APOD Basic HTML document                            |
| `BasicHTMLURL` | `basic_html_url` | Raw HTML route for copy/paste                            |

`FetchImage(ctx)` downloads `Hdurl`. Compared to the old API, `url` is now the
article page rather than the media file, and `service_version` is gone.

## CLI: afetch

`cmd/afetch` is its own Go module (it depends on `github.com/jackc/pgx/v5`
for the optional database export, so it's kept separate from the library's
dependencies). Build or run it from that directory:

```sh
cd cmd/afetch
go build .
./afetch -h
```

```sh
afetch -d 2023-07-20                                    # Get APOD for a specific date
afetch -d 230720                                        # Same, using the legacy YYMMDD code
afetch -dr "2023-07-01, 2023-07-07"                      # Get APOD for a date range
afetch -c 5                                              # Get 5 random APODs
afetch -d 2023-07-20 -dl                                 # Download the full-size image for a specific date
afetch -dr "2023-01-01, 2026-01-01" -concurrent 5        # Fetch a large range in concurrent batches
afetch -db "postgres://user:pass@localhost:5432/apod"    # Save fetched APOD data to PostgreSQL
```

Run `afetch -h` for the full flag list. Notable flags:

| Flag          | Description                                                           |
| ------------- | --------------------------------------------------------------------- |
| `-d`          | date for APOD, format `YYYY-MM-DD` or `YYMMDD` (or `"today"`)         |
| `-dr`         | date range, format `"YYYY-MM-DD, YYYY-MM-DD"`                         |
| `-c`          | number of random APODs to fetch                                       |
| `-concurrent` | number of concurrent requests when batching a large `-dr` range       |
| `-batch-size` | days per request when batching a large `-dr` range (max 25)           |
| `-dl`         | download the full-size image(s)                                       |
| `-db`         | PostgreSQL connection string to save results to (or `$DATABASE_URL`)  |

`-c` and `-db` are mutually exclusive.

The table schema is in `cmd/db_scripts/create_table.sql`. If you have a table
from the old API, `cmd/db_scripts/migrate_apod_basic.sql` adds the new columns
and drops `service_version`. Rows are upserted on `apod_date`, so re-fetching a
range refreshes existing rows and backfills the new columns.

## Development

```sh
go test ./...            # library tests run offline against a fake API server
cd cmd/afetch && go build .
```

## License

See [LICENSE.txt](LICENSE.txt).
