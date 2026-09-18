package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strings"

	"github.com/TWolfis/goapod"
	"github.com/jackc/pgx/v5"
)

const ProgramName = "afetch"

// Global variables for flags
var (
	opts Options
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
	Download    bool

	conn     *pgx.Conn
	dbBuffer []goapod.ApodResponse
}

// dbBatchSize is the number of responses buffered before WriteToDB flushes
// them as a single multi-row INSERT, instead of one round trip per response.
const dbBatchSize = 100

// apodFetchFunc fetches the responses for a single (non-batched) request.
type apodFetchFunc func(ctx context.Context, a *goapod.Apod) ([]goapod.ApodResponse, error)

// Fetch fetches the APOD data for the configured options, printing (and
// optionally downloading) each response as it arrives. Date, DateRange, and
// Count are mutually exclusive, so exactly one of the cases below applies.
func (o *Options) Fetch(ctx context.Context) error {
	a := goapod.NewApod(o.Date, o.DateRange, o.Count)
	if o.DatabaseURL != "" {
		if err := o.InitDBConn(ctx); err != nil {
			return fmt.Errorf("failed to initialize database connection: %w", err)
		}
		defer o.conn.Close(context.Background())
	}

	var err error
	switch {
	case !o.Date.IsZero():
		err = o.fetch(ctx, a, o.Date.Fetch)
	case o.DateRange.Len() > goapod.MaxPerPage:
		// More than one page of results: fetch the pages concurrently.
		err = o.fetchBatched(ctx, a)
	case !o.DateRange.StartDate.IsZero():
		err = o.fetch(ctx, a, o.DateRange.Fetch)
	default:
		err = o.fetch(ctx, a, o.Count.Fetch)
	}

	if o.conn != nil {
		o.flushDB()
	}
	return err
}

func (o *Options) InitDBConn(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, string(o.DatabaseURL))
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	o.conn = conn
	return nil
}

// WriteToDB upserts a batch of responses in a single multi-row INSERT
// instead of one round trip per response. Rows whose apod_date already
// exists are updated in place, so re-fetching an overlapping range is safe
// and backfills columns that an older fetch (or the old API) left empty.
func (o *Options) WriteToDB(resps []goapod.ApodResponse) error {
	if len(resps) == 0 {
		return nil
	}

	const cols = 11
	var sql strings.Builder
	sql.WriteString(`INSERT INTO apod (apod_date, post_id, title, explanation, credit, copyright, alt, media_type, url, hdurl, basic_html_url) VALUES `)

	args := make([]any, 0, len(resps)*cols)
	for i, resp := range resps {
		if i > 0 {
			sql.WriteString(", ")
		}
		sql.WriteString("(")
		for j := range cols {
			if j > 0 {
				sql.WriteString(", ")
			}
			fmt.Fprintf(&sql, "$%d", i*cols+j+1)
		}
		sql.WriteString(")")
		args = append(args,
			resp.Date, int64(resp.PostID), resp.Title, resp.PlainExplanation(), resp.PlainCredit(), goapod.StripHTML(resp.Copyright),
			resp.Alt, resp.MediaType, resp.URL, resp.Hdurl, resp.BasicHTMLURL)
	}
	sql.WriteString(` ON CONFLICT (apod_date) DO UPDATE SET
		post_id = EXCLUDED.post_id, title = EXCLUDED.title, explanation = EXCLUDED.explanation,
		credit = EXCLUDED.credit, copyright = EXCLUDED.copyright, alt = EXCLUDED.alt,
		media_type = EXCLUDED.media_type, url = EXCLUDED.url, hdurl = EXCLUDED.hdurl,
		basic_html_url = EXCLUDED.basic_html_url`)

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
		fmt.Fprintln(os.Stderr, "Error writing to database:", err)
	} else {
		fmt.Printf("%d APOD row(s) written to database successfully.\n", len(o.dbBuffer))
	}
	o.dbBuffer = o.dbBuffer[:0]
}

// fetch runs a single request and handles every response it returns.
func (o *Options) fetch(ctx context.Context, a *goapod.Apod, do apodFetchFunc) error {
	responses, err := do(ctx, a)
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
func (o *Options) fetchBatched(ctx context.Context, a *goapod.Apod) error {
	respChan, errChan := o.DateRange.FetchinBatches(ctx, a, o.Concurrent, o.BatchSize)

	var errs []error
	var cancelled bool
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
			// Once the context is done every remaining chunk reports the same
			// cancellation; keep it once rather than once per chunk.
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				cancelled = true
				continue
			}
			errs = append(errs, err)
		}
	}
	if cancelled {
		errs = append(errs, ctx.Err())
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
		if err := o.DownloadImage(context.Background(), resp); err != nil {
			fmt.Fprintln(os.Stderr, "Error downloading image:", err)
		} else {
			fmt.Println("Image downloaded successfully.")
		}
	}
}

// String returns a string representation of the APOD response
func (o *Options) String(a goapod.ApodResponse) string {
	return a.String()
}

// DownloadImage saves the APOD's full-size featured image to the current
// directory, named after its title with the image URL's file extension.
func (o *Options) DownloadImage(ctx context.Context, r goapod.ApodResponse) error {
	img, err := r.FetchImage(ctx)
	if err != nil {
		return err
	}

	ext := ".jpg"
	if u, err := url.Parse(r.Hdurl); err == nil {
		if e := path.Ext(u.Path); e != "" {
			ext = e
		}
	}

	return os.WriteFile(imageFilename(r, ext), img, 0o644)
}

// imageFilename derives a safe file name from the APOD title: lower-cased,
// spaces replaced by underscores, and any character that is not a letter,
// digit, dash, or underscore dropped. It falls back to the date when the
// title yields nothing usable.
func imageFilename(r goapod.ApodResponse, ext string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(r.Title) {
		switch {
		case c == ' ':
			b.WriteByte('_')
		case c == '-' || c == '_' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteRune(c)
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		name = r.Date
	}
	return name + ext
}

// Custom usage function for better help formatting
func init() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `afetch - NASA Astronomy Picture of the Day (APOD) Fetcher

Fetches the Astronomy Picture of the Day from NASA's APOD Basic API
(science.nasa.gov) for a single date, a date range, or a batch of random
dates. -d, -dr, and -c are mutually exclusive; with none set, afetch fetches
today's APOD. No API key is needed.

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
    %[1]s -d 140918                        # Same, using the legacy YYMMDD date code
    %[1]s -d 2023-07-20 -dl                # Download the full-size image for a specific date
    %[1]s -db "postgres://user:pass@localhost:5432/apod"  # Save APOD data to a PostgreSQL database
    %[1]s -dr "1995-06-16, today" -concurrent 20 -batch-size 15 -db "..."  # Fetch full history, streaming results as small chunks complete

ENVIRONMENT VARIABLES:
    DATABASE_URL    PostgreSQL database connection string (alternative to -db flag)

`, ProgramName)
	}

	// Seed the default database URL ($DATABASE_URL) up front, since flag.Var
	// only calls Set when -db is actually passed on the command line.
	opts.DatabaseURL.Set("")

	// Define all flags
	flag.BoolVar(&opts.Download, "dl", false, "download the full-size APOD image(s) to the current directory")
	flag.Var(&opts.Date, "d", `date for APOD, format YYYY-MM-DD or YYMMDD (or "today")`)
	flag.Var(&opts.DateRange, "dr", `date range for APOD, format "YYYY-MM-DD, YYYY-MM-DD"`)
	flag.Var(&opts.Count, "c", "number of random APODs to fetch")
	flag.IntVar(&opts.Concurrent, "concurrent", 10, "number of concurrent requests to make when batching a date range")
	flag.IntVar(&opts.BatchSize, "batch-size", goapod.MaximumApodRange, fmt.Sprintf("days per API request when batching a date range (max %d); smaller values stream results back sooner at the cost of more requests", goapod.MaximumApodRange))
	flag.Var((&opts.DatabaseURL), "db", "PostgreSQL database connection string (or set DATABASE_URL environment variable)")

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
		fmt.Fprintln(os.Stderr, "Error: -c and -db flags are mutually exclusive.")
		os.Exit(1)
	}

	// Set default date to today if no date, date range, or count is provided
	if opts.Date.IsZero() && opts.DateRange == (goapod.ApodDateRange{}) && opts.Count <= 0 {
		opts.Date.Set("today")
	}

	// Ctrl-C cancels in-flight requests instead of killing the process
	// mid-batch, so buffered database rows still get flushed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := opts.Fetch(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error fetching APOD data:", err)
		os.Exit(1)
	}
}
