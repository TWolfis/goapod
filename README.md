# GoApod

Golang library for fetching NASA's Astronomy Picture of the Day (APOD):
[api.nasa.gov/planetary/apod](https://api.nasa.gov/planetary/apod).

Fetch a single date, a date range, or a batch of random dates. Date ranges
longer than `goapod.MAXIMUM_APOD_RANGE` days can be fetched concurrently in
batches. The repo also includes `afetch`, a CLI built on top of the library.

## Installation

```sh
go get github.com/TWolfis/goapod
```

## Library usage

Every request starts from an `*Apod` built with `NewApod`, then dispatches to
the matching `Fetch` method. `ApodDate`, `ApodDateRange`, and `ApodCount` are
mutually exclusive — set only one.

```go
package main

import (
    "fmt"
    "log"

    "github.com/TWolfis/goapod"
)

func main() {
    apiKey := &goapod.ApodAPIKey{}
    apiKey.Set("") // falls back to $NASA_API_KEY, then "DEMO_KEY"

    var date goapod.ApodDate
    if err := date.Set("2023-07-20"); err != nil {
        log.Fatal(err)
    }

    a := goapod.NewApod(apiKey, date, goapod.ApodDateRange{}, 0, false)

    responses, err := date.Fetch(a)
    if err != nil {
        log.Fatal(err)
    }

    for _, r := range responses {
        fmt.Println(r.String())
    }
}
```

Fetching a date range works the same way, just set an `ApodDateRange` instead
and call `Fetch` on it:

```go
var dr goapod.ApodDateRange
dr.Set("2023-07-01, 2023-07-07") // "YYYY-MM-DD, YYYY-MM-DD"

a := goapod.NewApod(apiKey, goapod.ApodDate{}, dr, 0, false)
responses, err := dr.Fetch(a)
```

Ranges longer than `goapod.MAXIMUM_APOD_RANGE` days must go through
`FetchinBatches` instead, which splits the range into chunks and fetches them
concurrently, streaming results back over channels:

```go
respChan, errChan := dr.FetchinBatches(context.Background(), a, 5) // 5 concurrent requests
```

And fetching a batch of random dates:

```go
var count goapod.ApodCount
count.Set("5")

a := goapod.NewApod(apiKey, goapod.ApodDate{}, goapod.ApodDateRange{}, count, false)
responses, err := count.Fetch(a)
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
