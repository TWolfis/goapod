# GoApod

Golang library for fetching NASA's Astronomy Picture of the Day (APOD):
[api.nasa.gov/planetary/apod](https://api.nasa.gov/planetary/apod).

Fetch a single date, a date range, or a batch of random dates. Date ranges
longer than `goapod.MaximumApodRange` days can be fetched concurrently in
batches. The repo also includes `afetch`, a CLI built on top of the library.

## Installation

```sh
go get github.com/TWolfis/goapod
```

## Library usage

Every request starts from an `*Apod`, then dispatches to the matching `Fetch`
method. `ApodDate`, `ApodDateRange`, and `ApodCount` are mutually exclusive —
set only one. `goapod.New()` returns an `*Apod` with a default API key
(falls back to `$NASA_API_KEY`, then `"DEMO_KEY"`) and everything else left
at its zero value, ready to fill in:

```go
package main

import (
    "fmt"
    "log"

    "github.com/TWolfis/goapod"
)

func main() {
    a := goapod.New()

    if err := a.Date.Set("2023-07-20"); err != nil {
        log.Fatal(err)
    }

    responses, err := a.Date.Fetch(a)
    if err != nil {
        log.Fatal(err)
    }

    for _, r := range responses {
        fmt.Println(r.String())
    }
}
```

Fetching a date range works the same way, just set `DateRange` instead and
call `Fetch` on it:

```go
a := goapod.New()
a.DateRange.Set("2023-07-01, 2023-07-07") // "YYYY-MM-DD, YYYY-MM-DD"

responses, err := a.DateRange.Fetch(a)
```

Use `NewApod(apiKey, date, dateRange, count, thumbs)` instead of `New()` when
you already have a specific date, date range, or count and want the
mutually-exclusive combination validated up front (it panics on conflicting
fields, e.g. both `date` and `count` set).

Ranges longer than `goapod.MaximumApodRange` days must go through
`FetchinBatches` instead, which splits the range into chunks and fetches them
concurrently, streaming results back over channels:

```go
respChan, errChan := a.DateRange.FetchinBatches(context.Background(), a, 5, 30) // 5 concurrent requests, 30 days per request
```

And fetching a batch of random dates:

```go
a := goapod.New()
a.Count.Set("5")

responses, err := a.Count.Fetch(a)
```

`ApodAPIKey` also tracks the rate limit reported by NASA's API
(`RateLimit`/`RateLimitRemaining`, updated after every request) and returns an
error from `Fetch` once the limit is exhausted.

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
afetch -dr "2023-07-01, 2023-07-07"                      # Get APOD for a date range
afetch -c 5                                              # Get 5 random APODs
afetch -d 2023-07-20 -dl -hd                             # Download HD image for a specific date
afetch -dr "2023-01-01, 2026-01-01" -concurrent 5        # Fetch a large range in concurrent batches
afetch -db "postgres://user:pass@localhost:5432/apod"    # Save fetched APOD data to PostgreSQL
```

Run `afetch -h` for the full flag list. Notable flags:

| Flag          | Description                                                           |
| ------------- | --------------------------------------------------------------------- |
| `-d`          | date for APOD, format `YYYY-MM-DD` (or `"today"`)                     |
| `-dr`         | date range, format `"YYYY-MM-DD, YYYY-MM-DD"`                         |
| `-c`          | number of random APODs to fetch                                       |
| `-concurrent` | number of concurrent requests when batching a large `-dr` range       |
| `-thumbs`     | return video thumbnail URLs instead of the video URL                  |
| `-dl` / `-hd` | download the image(s); use the HD URL when downloading                |
| `-db`         | PostgreSQL connection string to save results to (or `$DATABASE_URL`)  |
| `-api-key`    | NASA API key (or `$NASA_API_KEY`, defaults to `DEMO_KEY`)             |

`-c` and `-db` are mutually exclusive.

## License

See [LICENSE.txt](LICENSE.txt).
