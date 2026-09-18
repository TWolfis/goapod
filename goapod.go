// Package goapod is a client for NASA's Astronomy Picture of the Day (APOD)
// "APOD Basic" JSON API served by science.nasa.gov:
// https://science.nasa.gov/wp-json/wp/v2/apod-basic.
//
// The APOD Basic API replaces the retired api.nasa.gov/planetary/apod
// service. It needs no API key, and it paginates list results at
// MaxPerPage items per request instead of accepting arbitrarily large date
// ranges. Dates in request URLs use the legacy YYMMDD code (LegacyDateFormat);
// dates in responses use YYYY-MM-DD (DateFormat).
//
// An Apod is fetched by a single date (ApodDate), a date range
// (ApodDateRange), or a batch of random dates (ApodCount) — these are
// mutually exclusive. Build one with New (defaults, fill in the rest
// yourself) or NewApod (validates a specific combination up front), then
// call Fetch on the relevant field (Date, DateRange, or Count). Every Fetch
// takes a context.Context for cancellation and deadlines.
// ApodDateRange.Fetch walks every page of a range sequentially;
// ApodDateRange.FetchinBatches fetches the range concurrently and streams
// results back over channels.
package goapod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"iter"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	FirstDate        = "1995-06-16" // First date of APOD
	DateFormat       = "2006-01-02" // Date format used in API responses and accepted by ApodDate.Set
	LegacyDateFormat = "060102"     // Legacy YYMMDD date code used in request URLs and query parameters

	MaxPerPage       = 25         // Maximum number of results the API returns per request (per_page is capped at this)
	MaximumApodRange = MaxPerPage // Maximum number of days per request in ApodDateRange.FetchinBatches; a chunk this size always fits on a single page

	BaseURL        = "https://science.nasa.gov/wp-json/wp/v2/apod-basic" // Base URL for the APOD Basic API
	DefaultTimeout = 60 * time.Second                                    // Timeout of the http.Client created by New

	// RandomConcurrency is the number of single-date requests ApodCount.Fetch
	// keeps in flight at once. The API has no random endpoint, so random
	// APODs are emulated with one request per date.
	RandomConcurrency = 5
)

// Apod contains the variables associated with the APOD API
type Apod struct {
	Client    *http.Client  // Client used for every request; New sets one with DefaultTimeout, nil falls back to http.DefaultClient
	BaseURL   string        // BaseURL of the APOD Basic API; New sets it to BaseURL, empty falls back to BaseURL
	Date      ApodDate      `json:"date"`       // Date of the Apod image to retrieve defaults to today
	DateRange ApodDateRange `json:"date_range"` // Range of date ranges, when requesting for a range of dates. Cannot be used with Date, defaults to none
	Count     ApodCount     `json:"count"`      // Count If this is specified then count randomly chosen images will be returned. Cannot be used with date or StartDate and EndDate, defaults to none
}

// New returns an Apod with a default HTTP client and base URL, and Date,
// DateRange, and Count left at their zero values. Set exactly one of them
// (e.g. a.Date.Set("2023-07-20")) and call Fetch on that field. Use NewApod
// instead when you already have a specific date, date range, or count to
// validate up front.
func New() *Apod {
	return &Apod{
		Client:  &http.Client{Timeout: DefaultTimeout},
		BaseURL: BaseURL,
	}
}

// NewApod validates the given combination of date, date range, and count
// parameters (which are mutually exclusive) and returns a configured Apod.
func NewApod(date ApodDate, dateRange ApodDateRange, count ApodCount) *Apod {
	hasDate := !date.IsZero()
	hasDateRange := !dateRange.StartDate.IsZero() || !dateRange.EndDate.IsZero()
	hasCount := count > 0

	switch {
	case hasDate && hasDateRange:
		panic("cannot use date with start_date or end_date")
	case hasDate && hasCount:
		panic("cannot use date with count")
	case hasDateRange && hasCount:
		panic("cannot use start_date or end_date with count")
	}

	if !dateRange.StartDate.IsZero() && dateRange.EndDate.IsZero() {
		dateRange.EndDate = getTodayDate()
	}

	if dateRange.StartDate.IsZero() && !dateRange.EndDate.IsZero() {
		dateRange.StartDate = firstApodDate()
	}

	a := New()
	a.Date = date
	a.DateRange = dateRange
	a.Count = count
	return a
}

// APIError is an error response from the API, decoded from the WordPress
// REST error body ({"code": ..., "message": ..., "data": {"status": ...}}).
type APIError struct {
	StatusCode int    // HTTP status code of the response
	Code       string `json:"code"`    // Machine-readable error code, e.g. "apod_basic_not_found" or "rest_invalid_param"
	Message    string `json:"message"` // Human-readable message
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("API request failed with status code: %d", e.StatusCode)
	}
	return fmt.Sprintf("API request failed with status code %d: %s (%s)", e.StatusCode, e.Message, e.Code)
}

// NotFound reports whether the error means no APOD exists for the requested
// date (HTTP 404).
func (e *APIError) NotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

func (a *Apod) httpClient() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return http.DefaultClient
}

func (a *Apod) baseURL() string {
	if a.BaseURL != "" {
		return a.BaseURL
	}
	return BaseURL
}

// get performs a GET request against path (relative to the base URL) with
// the given query and returns the response body and headers. A non-200
// response is returned as an *APIError.
func (a *Apod) get(ctx context.Context, path string, query url.Values) ([]byte, http.Header, error) {
	u, err := url.Parse(a.baseURL())
	if err != nil {
		return nil, nil, err
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	if resp.StatusCode != http.StatusOK {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		_ = json.Unmarshal(body, apiErr) // best effort; a non-JSON body still yields a useful error
		return nil, resp.Header, apiErr
	}
	return body, resp.Header, nil
}

// fetchDate retrieves the APOD for a single date via /apod-basic/{YYMMDD}.
func (a *Apod) fetchDate(ctx context.Context, d ApodDate) (ApodResponse, error) {
	body, _, err := a.get(ctx, "/"+d.Legacy(), nil)
	if err != nil {
		return ApodResponse{}, err
	}
	var resp ApodResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ApodResponse{}, err
	}
	return resp, nil
}

// fetchPage retrieves one page of a date range and reports the total number
// of pages (from the X-WP-TotalPages header; 0 if absent). Results are
// returned in the API's order: newest first.
func (a *Apod) fetchPage(ctx context.Context, dr ApodDateRange, page, perPage int) ([]ApodResponse, int, error) {
	query := url.Values{}
	query.Set("date_from", dr.StartDate.Legacy())
	query.Set("date_to", dr.EndDate.Legacy())
	query.Set("page", strconv.Itoa(page))
	query.Set("per_page", strconv.Itoa(perPage))

	body, header, err := a.get(ctx, "", query)
	if err != nil {
		return nil, 0, err
	}

	var resps []ApodResponse
	if err := json.Unmarshal(body, &resps); err != nil {
		return nil, 0, err
	}

	totalPages, _ := strconv.Atoi(header.Get("X-WP-TotalPages"))
	return resps, totalPages, nil
}

// fetchRange retrieves every APOD in the range by walking all of its pages
// sequentially.
func (a *Apod) fetchRange(ctx context.Context, dr ApodDateRange) ([]ApodResponse, error) {
	var all []ApodResponse
	for page := 1; ; page++ {
		resps, totalPages, err := a.fetchPage(ctx, dr, page, MaxPerPage)
		if err != nil {
			return all, err
		}
		all = append(all, resps...)
		if len(resps) == 0 || page >= totalPages {
			return all, nil
		}
	}
}

func getTodayDate() ApodDate {
	return ApodDate{Time: time.Now()}
}

// firstApodDate returns the earliest valid APOD date (FirstDate) as an ApodDate.
func firstApodDate() ApodDate {
	t, _ := time.Parse(DateFormat, FirstDate)
	return ApodDate{Time: t}
}

// ApodDate is a custom type for handling date parsing and validation
type ApodDate struct {
	time.Time
}

// String returns the string representation of the date
func (d ApodDate) String() string {
	return d.Format(DateFormat)
}

// Legacy returns the date as the legacy YYMMDD code used in request URLs.
func (d ApodDate) Legacy() string {
	return d.Format(LegacyDateFormat)
}

// Set parses and sets the date value. It accepts "today", YYYY-MM-DD, or
// the legacy YYMMDD code.
func (d *ApodDate) Set(value string) error {
	if value == "today" {
		*d = getTodayDate()
		return nil
	}

	t, err := time.Parse(DateFormat, value)
	if err != nil {
		if lt, lerr := time.Parse(LegacyDateFormat, value); lerr == nil && len(value) == len(LegacyDateFormat) {
			t = lt
		} else {
			return fmt.Errorf("invalid date format: %s, expected YYYY-MM-DD or YYMMDD", value)
		}
	}

	if t.Before(firstApodDate().Time) {
		return fmt.Errorf("date cannot be before %s", FirstDate)
	}

	d.Time = t
	return nil
}

// Fetch retrieves the APOD for this single date. The returned slice holds
// exactly one response on success; a missing date yields an *APIError whose
// NotFound method reports true.
func (d *ApodDate) Fetch(ctx context.Context, a *Apod) ([]ApodResponse, error) {
	resp, err := a.fetchDate(ctx, *d)
	if err != nil {
		return nil, err
	}
	return []ApodResponse{resp}, nil
}

// ApodDateRange is a custom type for handling date range parsing and validation
type ApodDateRange struct {
	StartDate ApodDate // StartDate is the first day of the range, inclusive
	EndDate   ApodDate // EndDate is the last day of the range, inclusive

	len int
}

// String returns the string representation of the date range
func (dr *ApodDateRange) String() string {
	return fmt.Sprintf("%s to %s", dr.StartDate, dr.EndDate)
}

// Set parses and sets the date range value
func (dr *ApodDateRange) Set(value string) error {
	// Split the value into start and end dates
	dates := strings.Split(value, ", ")
	if len(dates) != 2 {
		return fmt.Errorf("invalid date range format: %s, expected start_date,end_date", value)
	}

	var startDate, endDate ApodDate
	if err := startDate.Set(dates[0]); err != nil {
		return fmt.Errorf("invalid start date: %w", err)
	}
	if err := endDate.Set(dates[1]); err != nil {
		return fmt.Errorf("invalid end date: %w", err)
	}

	if startDate.After(endDate.Time) {
		return fmt.Errorf("start date cannot be after end date")
	}

	dr.StartDate = startDate
	dr.EndDate = endDate

	dr.len = 0
	for range dr.All() {
		dr.len++
	}

	return nil
}

// All returns an iterator over every date in the range, formatted as
// YYYY-MM-DD, from StartDate to EndDate inclusive.
func (dr *ApodDateRange) All() iter.Seq[string] {
	return func(yield func(string) bool) {
		for d := dr.StartDate; !d.After(dr.EndDate.Time); d = (ApodDate{Time: d.AddDate(0, 0, 1)}) {
			if !yield(d.String()) {
				break
			}
		}
	}
}

// Len reports the number of days in the range.
func (dr *ApodDateRange) Len() int {
	return dr.len
}

// chunks the date range into smaller ranges of the specified size
func (dr *ApodDateRange) chunk(size int) []ApodDateRange {
	var chunks []ApodDateRange
	currentStart := dr.StartDate

	for currentStart.Before(dr.EndDate.Time) || currentStart.Equal(dr.EndDate.Time) {
		currentEnd := ApodDate{Time: currentStart.AddDate(0, 0, size-1)}
		if currentEnd.After(dr.EndDate.Time) {
			currentEnd = dr.EndDate
		}

		chunks = append(chunks, ApodDateRange{
			StartDate: currentStart,
			EndDate:   currentEnd,
		})

		currentStart = ApodDate{Time: currentEnd.AddDate(0, 0, 1)}
	}

	return chunks
}

// Fetch retrieves the APOD for every date in the range, walking all of the
// range's pages one after another (MaxPerPage results per request). Results
// are in the API's order, newest first. For large ranges, FetchinBatches
// fetches concurrently and streams results as they arrive.
func (dr *ApodDateRange) Fetch(ctx context.Context, a *Apod) ([]ApodResponse, error) {
	return a.fetchRange(ctx, *dr)
}

// FetchinBatches fetches a date range by splitting it into chunks of
// batchSize days (clamped to at most MaximumApodRange, so every chunk fits
// in a single API request) and fetching up to concurrent chunks at a time.
// A response isn't visible on the returned channel until its whole chunk's
// API call completes, so a smaller batchSize trades more total requests for
// more frequent, incremental results instead of long silences while a large
// chunk is in flight. Responses and errors are delivered on the returned
// channels as they complete; both channels are closed once every chunk has
// been processed.
func (dr *ApodDateRange) FetchinBatches(ctx context.Context, a *Apod, concurrent int, batchSize int) (<-chan ApodResponse, <-chan error) {
	if batchSize <= 0 || batchSize > MaximumApodRange {
		batchSize = MaximumApodRange
	}
	if concurrent <= 0 {
		concurrent = 1
	}

	chunks := dr.chunk(batchSize)

	sem := make(chan struct{}, concurrent)
	respChan := make(chan ApodResponse, len(chunks))
	errChan := make(chan error, len(chunks))

	var wg sync.WaitGroup

	for _, chunk := range chunks {
		wg.Add(1)
		go func(chunk ApodDateRange) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			}
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			default:
			}

			resps, err := a.fetchRange(ctx, chunk)
			if err != nil {
				errChan <- fmt.Errorf("error fetching APOD data for %s: %w", chunk.String(), err)
				return
			}

			for _, resp := range resps {
				respChan <- resp
			}
		}(chunk)
	}
	go func() {
		wg.Wait()
		close(respChan)
		close(errChan)
	}()

	return respChan, errChan
}

// ApodCount is the number of randomly chosen APODs to fetch. It is
// mutually exclusive with ApodDate and ApodDateRange.
type ApodCount int

// String returns the string representation of the count.
func (c *ApodCount) String() string {
	return strconv.Itoa(int(*c))
}

// Set parses and sets the count value; it must be a positive integer.
func (c *ApodCount) Set(value string) error {
	count, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid count value: %s", value)
	}
	if count <= 0 {
		return fmt.Errorf("count must be a positive integer")
	}
	*c = ApodCount(count)
	return nil
}

// Fetch retrieves Count randomly chosen APODs. The API has no random
// endpoint, so distinct random dates between FirstDate and today are drawn
// and fetched individually, RandomConcurrency requests at a time. Dates
// with no APOD are skipped and redrawn.
func (c *ApodCount) Fetch(ctx context.Context, a *Apod) ([]ApodResponse, error) {
	n := int(*c)
	if n <= 0 {
		return nil, errors.New("count must be a positive integer")
	}

	first := firstApodDate()
	days := int(getTodayDate().Sub(first.Time).Hours()/24) + 1
	if n > days {
		return nil, fmt.Errorf("count %d exceeds the number of days since %s", n, FirstDate)
	}

	tried := make(map[int]bool)
	var results []ApodResponse
	var errs []error

	// Work in rounds: each round draws one fresh date per missing result
	// and fetches them concurrently. Dates without an APOD are dropped, so
	// the next round redraws for whatever is still missing. Rounds stop once
	// n results are in, a request fails, or the attempt budget is spent.
	maxAttempts := min(n*4, days)
	attempts := 0
	for len(results) < n && attempts < maxAttempts && len(errs) == 0 {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		missing := min(n-len(results), maxAttempts-attempts)
		dates := make([]ApodDate, 0, missing)
		for len(dates) < missing {
			offset := rand.IntN(days)
			if tried[offset] {
				continue
			}
			tried[offset] = true
			dates = append(dates, ApodDate{Time: first.AddDate(0, 0, offset)})
		}
		attempts += len(dates)

		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, RandomConcurrency)
		for _, d := range dates {
			wg.Add(1)
			go func(d ApodDate) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				resp, err := a.fetchDate(ctx, d)

				mu.Lock()
				defer mu.Unlock()
				var apiErr *APIError
				switch {
				case err == nil:
					results = append(results, resp)
				case errors.As(err, &apiErr) && apiErr.NotFound():
					// No APOD on this day; the next round redraws.
				default:
					errs = append(errs, fmt.Errorf("error fetching APOD data for %s: %w", d, err))
				}
			}(d)
		}
		wg.Wait()
	}

	if len(results) < n && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("only found %d of %d random APODs after %d attempts", len(results), n, attempts))
	}
	return results, errors.Join(errs...)
}

// PostID is a WordPress post ID. The API has returned it both as a JSON
// number and as a JSON string, so both forms are accepted.
type PostID int64

// UnmarshalJSON implements json.Unmarshaler.
func (p *PostID) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	if s == "" || s == "null" {
		*p = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid post_id %s: %w", string(data), err)
	}
	*p = PostID(n)
	return nil
}

// String returns the decimal representation of the post ID.
func (p PostID) String() string {
	return strconv.FormatInt(int64(p), 10)
}

// ApodResponse holds the response JSON object received by a successful call
// to the APOD Basic API. Explanation, Credit, and Copyright contain HTML
// fragments; see PlainExplanation for a text-only explanation.
type ApodResponse struct {
	Date         string `json:"date"`           // APOD date in YYYY-MM-DD format
	PostID       PostID `json:"post_id"`        // WordPress post ID
	Title        string `json:"title"`          // APOD post title
	Permalink    string `json:"permalink"`      // URL of the APOD post on science.nasa.gov
	MediaType    string `json:"media_type"`     // Normalized media type: "image", "video", or "iframe"
	Explanation  string `json:"explanation"`    // APOD explanation as an HTML fragment
	Credit       string `json:"credit"`         // Image credit as an HTML fragment
	Copyright    string `json:"copyright"`      // Copyright as an HTML fragment (currently identical to Credit)
	Alt          string `json:"alt"`            // Alt text of the featured image
	URL          string `json:"url"`            // APOD post URL; matches Permalink
	Hdurl        string `json:"hdurl"`          // Full-size featured image URL when available (a poster frame for videos)
	BasicHTML    string `json:"basic_html"`     // APOD Basic HTML document
	BasicHTMLURL string `json:"basic_html_url"` // URL of the raw APOD Basic HTML document
}

var (
	htmlTagRe        = regexp.MustCompile(`<[^>]*>`)
	explanationLabel = regexp.MustCompile(`(?i)^\s*explanation:\s*`)
)

// StripHTML removes HTML tags from s, unescapes HTML entities, and collapses
// runs of whitespace into single spaces.
func StripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.Join(strings.Fields(s), " ")
}

// PlainExplanation returns the explanation with HTML removed and the leading
// "Explanation:" label dropped, matching the plain text the retired
// api.nasa.gov API used to return.
func (a *ApodResponse) PlainExplanation() string {
	return explanationLabel.ReplaceAllString(StripHTML(a.Explanation), "")
}

// PlainCredit returns the credit with HTML removed.
func (a *ApodResponse) PlainCredit() string {
	return StripHTML(a.Credit)
}

// String returns a human-readable summary of the APOD response.
func (a *ApodResponse) String() string {
	return fmt.Sprintf("Date: %s\nTitle: %s\nExplanation: %s\nCredit: %s\nMedia Type: %s\nURL: %s\nHD URL: %s\nPost ID: %s\n",
		a.Date, a.Title, a.PlainExplanation(), a.PlainCredit(), a.MediaType, a.URL, a.Hdurl, a.PostID)
}

// FetchImage downloads the full-size featured image (Hdurl). Video and
// iframe APODs usually still have a featured image, which is what is
// downloaded for them; an error is returned when no Hdurl is available.
func (a *ApodResponse) FetchImage(ctx context.Context) ([]byte, error) {
	if a.Hdurl == "" {
		return nil, fmt.Errorf("apod %s has no featured image (hdurl)", a.Date)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Hdurl, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: DefaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image request failed with status code: %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
