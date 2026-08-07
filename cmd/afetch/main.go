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

const PROGRAM_NAME = "afetch"

// Global variables for flags
var (
	opts = Options{ApiKey: &goapod.ApodAPIKey{}}
	help bool
)

type DatabaseURLFlag string

func (d DatabaseURLFlag) String() string {
	return string(d)
}

func (d *DatabaseURLFlag) Set(value string) error {
	if value == "" {

		// try to get the database connection string from the environment variable
		value, exists := os.LookupEnv("DATABASE_URL")
		if !exists || value == "" {
			return errors.New("database connection string cannot be empty")
		}
	}
	*d = DatabaseURLFlag(value)
	return nil
}

type Options struct {
	DatabaseUrl DatabaseURLFlag
	Date        goapod.ApodDate
	DateRange   goapod.ApodDateRange
	Count       goapod.ApodCount
	Concurrent  int
	Thumbs      bool
	Download    bool
	Hdurl       bool
	ApiKey      *goapod.ApodAPIKey

	conn *pgx.Conn
}

// apodFetchFunc fetches the responses for a single (non-batched) request.
type apodFetchFunc func(a *goapod.Apod) ([]goapod.ApodResponse, error)

// Fetch fetches the APOD data for the configured options, printing (and
// optionally downloading) each response as it arrives. Date, DateRange, and
// Count are mutually exclusive, so exactly one of the cases below applies.
func (o *Options) Fetch() error {
	a := goapod.NewApod(o.ApiKey, o.Date, o.DateRange, o.Count, o.Thumbs)
	if o.DatabaseUrl != "" {
		if err := o.InitDbConn(); err != nil {
			return fmt.Errorf("failed to initialize database connection: %w", err)
		}
		defer o.conn.Close(context.Background())
	}

	switch {
	case !o.Date.IsZero():
		return o.fetch(a, o.Date.Fetch)
	case o.DateRange.Len() > goapod.MAXIMUM_APOD_RANGE:
		return o.fetchBatched(a)
	case !o.DateRange.StartDate.IsZero():
		return o.fetch(a, o.DateRange.Fetch)
	default:
		return o.fetch(a, o.Count.Fetch)
	}
}

func (o *Options) InitDbConn() error {
	var conn *pgx.Conn
	conn, err := pgx.Connect(context.Background(), string(o.DatabaseUrl))
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	o.conn = conn
	return nil
}

func (o *Options) WriteToDb(resp goapod.ApodResponse) error {
	_, err := o.conn.Exec(context.Background(),
		`INSERT INTO apod (apod_date, title, explanation, media_type, url, hdurl, service_version) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		resp.Date, resp.Title, resp.Explanation, resp.MediaType, resp.URL, resp.Hdurl, resp.ServiceVersion)
	if err != nil {
		return fmt.Errorf("failed to insert APOD data into database: %w", err)
	}
	return nil
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
	respChan, errChan := o.DateRange.FetchinBatches(context.Background(), a, o.Concurrent)

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

	// If a database connection is provided, write the response to the database
	if o.conn != nil {
		if err := o.WriteToDb(resp); err != nil {
			fmt.Println("Error writing to database:", err)
		} else {
			fmt.Println("APOD data written to database successfully.")
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

// SaveImage saves the APOD image to the specified destination file
func (o *Options) DownloadImage(r goapod.ApodResponse) error {
	f := strings.ToLower(r.Title) + ".jpg"
	f = strings.ReplaceAll(f, " ", "_")
	img, err := r.FetchImage(o.Hdurl)
	if err != nil {
		return err
	}

	os.WriteFile(f, img, 0644)
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
`, PROGRAM_NAME)
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), `
EXAMPLES:
    %[1]s                                  # Get today's APOD
    %[1]s -d 2023-07-20                    # Get APOD for a specific date
    %[1]s -dr "2023-07-01, 2023-07-07"     # Get APOD for a date range
    %[1]s -c 5                             # Get 5 random APODs
    %[1]s -d 2023-07-20 -dl -hd            # Download HD image for a specific date
	%[1]s -db "postgres://user:pass@localhost:5432/apod"  # Save APOD data to a PostgreSQL database

ENVIRONMENT VARIABLES:
    NASA_API_KEY               NASA API key for APOD service (alternative to -api-key flag)
	DATABASE_URL               PostgreSQL database connection string (alternative to -db flag)

`, PROGRAM_NAME)
	}

	// Define all flags
	flag.BoolVar(&opts.Hdurl, "hd", true, "use the HD image URL when downloading")
	flag.BoolVar(&opts.Download, "dl", false, "download the APOD image(s) to the current directory")
	flag.Var(&opts.Date, "d", `date for APOD, format YYYY-MM-DD (or "today")`)
	flag.Var(&opts.DateRange, "dr", `date range for APOD, format "YYYY-MM-DD, YYYY-MM-DD"`)
	flag.Var(&opts.Count, "c", "number of random APODs to fetch")
	flag.IntVar(&opts.Concurrent, "concurrent", 2, "number of concurrent requests to make")
	flag.BoolVar(&opts.Thumbs, "thumbs", false, "return video thumbnail URLs instead of the video URL")
	flag.Var((&opts.DatabaseUrl), "db", "PostgreSQL database connection string (or set DATABASE_URL environment variable)")
	flag.Var(opts.ApiKey, "api-key", "NASA API key for APOD service (defaults to $NASA_API_KEY, then DEMO_KEY)")

	flag.BoolVar(&help, "h", false, "show this help message")
}

func main() {

	flag.Parse()

	if help {
		flag.Usage()
		os.Exit(0)
	}

	// Count and Database URL are mutually exclusive; if both are set, print an error and exit
	if opts.Count > 0 && opts.DatabaseUrl != "" {
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
