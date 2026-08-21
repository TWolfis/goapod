package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/TWolfis/goapod"
	"github.com/jackc/pgx/v5"
)

const ProgramName = "afetch"

// Global variables for flags
var (
	opts = Options{APIKey: &goapod.ApodAPIKey{}}
	help bool
)

type DatabaseURLFlag string

func (d DatabaseURLFlag) String() string {
	return string(d)
}

func (d *DatabaseURLFlag) Set(value string) error {
	if value == "" {

		// try to get the database connection string from the environment variable
		envValue, exists := os.LookupEnv("DATABASE_URL")
		if !exists || envValue == "" {
			return errors.New("database connection string cannot be empty")
		}
		value = envValue
	}
	*d = DatabaseURLFlag(value)
	return nil
}

type Options struct {
	DatabaseURL DatabaseURLFlag
	Date        goapod.ApodDate
	DateRange   goapod.ApodDateRange
	Count       goapod.ApodCount
	Concurrent  int
	BatchSize   int
	Thumbs      bool
	Download    bool
	Hdurl       bool
	APIKey      *goapod.ApodAPIKey

	conn     *pgx.Conn
	dbBuffer []goapod.ApodResponse
}

// dbBatchSize is the number of responses buffered before WriteToDB flushes
// them as a single multi-row INSERT, instead of one round trip per response.
const dbBatchSize = 100

// apodFetchFunc fetches the responses for a single (non-batched) request.
type apodFetchFunc func(a *goapod.Apod) ([]goapod.ApodResponse, error)

// Fetch fetches the APOD data for the configured options, printing (and
// optionally downloading) each response as it arrives. Date, DateRange, and
// Count are mutually exclusive, so exactly one of the cases below applies.
func (o *Options) Fetch() error {
	a := goapod.NewApod(o.APIKey, o.Date, o.DateRange, o.Count, o.Thumbs)
	if o.DatabaseURL != "" {
		if err := o.InitDBConn(); err != nil {
			return fmt.Errorf("failed to initialize database connection: %w", err)
		}
		defer o.conn.Close(context.Background())
	}

	var err error
	switch {
	case !o.Date.IsZero():
		err = o.fetch(a, o.Date.Fetch)
	case o.DateRange.Len() > goapod.MaximumApodRange:
		err = o.fetchBatched(a)
	case !o.DateRange.StartDate.IsZero():
		err = o.fetch(a, o.DateRange.Fetch)
	default:
		err = o.fetch(a, o.Count.Fetch)
	}

	if o.conn != nil {
		o.flushDB()
	}
	return err
}

func (o *Options) InitDBConn() error {
	var conn *pgx.Conn
	conn, err := pgx.Connect(context.Background(), string(o.DatabaseURL))
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	o.conn = conn
	return nil
}

// WriteToDB inserts a batch of responses in a single multi-row INSERT
// instead of one round trip per response. Rows whose apod_date already
// exists are silently skipped, so re-fetching an overlapping range is safe.
func (o *Options) WriteToDB(resps []goapod.ApodResponse) error {
	if len(resps) == 0 {
		return nil
	}

	var sql strings.Builder
	sql.WriteString(`INSERT INTO apod (apod_date, title, explanation, media_type, url, hdurl, service_version) VALUES `)

	args := make([]any, 0, len(resps)*7)
	for i, resp := range resps {
		if i > 0 {
			sql.WriteString(", ")
		}
		n := i * 7
		fmt.Fprintf(&sql, "($%d, $%d, $%d, $%d, $%d, $%d, $%d)", n+1, n+2, n+3, n+4, n+5, n+6, n+7)
		args = append(args, resp.Date, resp.Title, resp.Explanation, resp.MediaType, resp.URL, resp.Hdurl, resp.ServiceVersion)
	}
	sql.WriteString(" ON CONFLICT (apod_date) DO NOTHING")

	if _, err := o.conn.Exec(context.Background(), sql.String(), args...); err != nil {
		return fmt.Errorf("failed to insert APOD data into database: %w", err)
	}
	return nil
}

// flushDB writes the buffered responses to the database and clears the
// buffer, regardless of how many are currently pending.
func (o *Options) flushDB() {
	if len(o.dbBuffer) == 0 {
		return
	}
	if err := o.WriteToDB(o.dbBuffer); err != nil {
		fmt.Println("Error writing to database:", err)
	} else {
		fmt.Printf("%d APOD row(s) written to database successfully.\n", len(o.dbBuffer))
	}
	o.dbBuffer = o.dbBuffer[:0]
}

// fetch runs a single request and handles every response it returns.
func (o *Options) fetch(a *goapod.Apod, do apodFetchFunc) error {
	responses, err := do(a)
	if err != nil {
		return err
	}

	for _, resp := range responses {
		o.handle(resp)
	}
	return nil
}

// fetchBatched fetches a date range too large for a single request in
// concurrent batches, handling each response as it streams in.
func (o *Options) fetchBatched(a *goapod.Apod) error {
	respChan, errChan := o.DateRange.FetchinBatches(context.Background(), a, o.Concurrent, o.BatchSize)

	var errs []error
	for respChan != nil || errChan != nil {
		select {
		case resp, ok := <-respChan:
			if !ok {
				respChan = nil
				continue
			}
			o.handle(resp)
		case err, ok := <-errChan:
			if !ok {
				errChan = nil
				continue
			}
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// handle prints a single APOD response and downloads its image if requested.
func (o *Options) handle(resp goapod.ApodResponse) {
	fmt.Println(o.String(resp))

	// If a database connection is provided, buffer the response and flush
	// once a full batch has accumulated (Fetch flushes any remainder).
	if o.conn != nil {
		o.dbBuffer = append(o.dbBuffer, resp)
		if len(o.dbBuffer) >= dbBatchSize {
			o.flushDB()
		}
	}

	if o.Download {
		if err := o.DownloadImage(resp); err != nil {
			fmt.Println("Error downloading image:", err)
		} else {
			fmt.Println("Image downloaded successfully.")
		}
	}
}

// String returns a string representation of the APOD response
func (o *Options) String(a goapod.ApodResponse) string {
	return a.String()
}

// DownloadImage saves the APOD image to the current directory, named after its title.
func (o *Options) DownloadImage(r goapod.ApodResponse) error {
	f := strings.ToLower(r.Title) + ".jpg"
	f = strings.ReplaceAll(f, " ", "_")
	img, err := r.FetchImage(o.Hdurl)
	if err != nil {
		return err
	}

	os.WriteFile(f, img, 0o644)
	return nil
}

// Custom usage function for better help formatting
func init() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `afetch - NASA Astronomy Picture of the Day (APOD) Fetcher

Fetches the Astronomy Picture of the Day from NASA's APOD API for a single
date, a date range, or a batch of random dates. -d, -dr, and -c are mutually
exclusive; with none set, afetch fetches today's APOD.

USAGE:
    %[1]s [FLAGS]

FLAGS:
`, ProgramName)
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), `
EXAMPLES:
    %[1]s                                  # Get today's APOD
    %[1]s -d 2023-07-20                    # Get APOD for a specific date
    %[1]s -dr "2023-07-01, 2023-07-07"     # Get APOD for a date range
    %[1]s -c 5                             # Get 5 random APODs
    %[1]s -d 2023-07-20 -dl -hd            # Download HD image for a specific date
    %[1]s -db "postgres://user:pass@localhost:5432/apod"  # Save APOD data to a PostgreSQL database
    %[1]s -dr "1995-06-16, today" -concurrent 20 -batch-size 15 -db "..."  # Fetch full history, streaming results as small chunks complete

ENVIRONMENT VARIABLES:
    NASA_API_KEY    NASA API key for APOD service (alternative to -api-key flag)
    DATABASE_URL    PostgreSQL database connection string (alternative to -db flag)

`, ProgramName)
	}

	// Seed the default API key ($NASA_API_KEY, falling back to "DEMO_KEY") up
	// front, since flag.Var only calls Set when -api-key is actually passed.
	opts.APIKey.Set("")

	// Define all flags
	flag.BoolVar(&opts.Hdurl, "hd", true, "use the HD image URL when downloading")
	flag.BoolVar(&opts.Download, "dl", false, "download the APOD image(s) to the current directory")
	flag.Var(&opts.Date, "d", `date for APOD, format YYYY-MM-DD (or "today")`)
	flag.Var(&opts.DateRange, "dr", `date range for APOD, format "YYYY-MM-DD, YYYY-MM-DD"`)
	flag.Var(&opts.Count, "c", "number of random APODs to fetch")
	flag.IntVar(&opts.Concurrent, "concurrent", 10, "number of concurrent requests to make when batching a date range")
	flag.IntVar(&opts.BatchSize, "batch-size", 30, fmt.Sprintf("days per API request when batching a date range (max %d); smaller values stream results back sooner at the cost of more requests", goapod.MaximumApodRange))
	flag.BoolVar(&opts.Thumbs, "thumbs", false, "return video thumbnail URLs instead of the video URL")
	flag.Var((&opts.DatabaseURL), "db", "PostgreSQL database connection string (or set DATABASE_URL environment variable)")
	flag.Var(opts.APIKey, "api-key", "NASA API key for APOD service (defaults to $NASA_API_KEY, then DEMO_KEY)")

	flag.BoolVar(&help, "h", false, "show this help message")
}

func main() {
	flag.Parse()

	if help {
		flag.Usage()
		os.Exit(0)
	}

	// Count and Database URL are mutually exclusive; if both are set, print an error and exit
	if opts.Count > 0 && opts.DatabaseURL != "" {
		fmt.Println("Error: -c and -db flags are mutually exclusive.")
		os.Exit(1)
	}

	// Set default date to today if no date, date range, or count is provided
	if opts.Date.IsZero() && opts.DateRange == (goapod.ApodDateRange{}) && opts.Count <= 0 {
		opts.Date.Set("today")
	}

	if err := opts.Fetch(); err != nil {
		fmt.Println("Error fetching APOD data:", err)
		os.Exit(1)
	}
}
