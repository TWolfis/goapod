// Package goapod is a client for NASA's Astronomy Picture of the Day (APOD)
// API: https://api.nasa.gov/planetary/apod.
//
// An Apod is fetched by a single date (ApodDate), a date range
// (ApodDateRange), or a batch of random dates (ApodCount) — these are
// mutually exclusive. Build one with New (defaults, fill in the rest
// yourself) or NewApod (validates a specific combination up front), then
// call Fetch on the relevant field (Date, DateRange, or Count). Date ranges
// longer than MaximumApodRange must use ApodDateRange.FetchinBatches
// instead of ApodDateRange.Fetch.
package goapod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	FirstDate  = "1995-06-16" // First date of APOD
	DateFormat = "2006-01-02" // Date format for APOD

	MaximumApodRange = 300  // Maximum number of days that can be requested in a single API call
	DemoKeyLimit     = 30   // Hourly rate limit for the default "DEMO_KEY" API key
	APIKeyLimit      = 1000 // Hourly rate limit for a personal API key

	BaseURL = "https://api.nasa.gov/planetary/apod" // Base URL for the APOD API
)

// Apod contains the variables associated with the APOD API
type Apod struct {
	APIKey    *ApodAPIKey   // APIKey is the user's personal API key, defaults to "DEMO_KEY"
	Date      ApodDate      `json:"date"`       // Date of the Apod image to retrieve defaults to today
	DateRange ApodDateRange `json:"date_range"` // Range of date ranges, when requesting for a range of dates. Cannot be used with Date, defaults to none
	Count     ApodCount     `json:"count"`      // Count If this is specified then count randomly chosen images will be returned. Cannot be used with date or StartDate and EndDate, defaults to none
	Thumbs    bool          `json:"thumbs"`     // Thumbs Return the URL of video thumbnail. If an Apod is not a video, this parameter is ignored, defaults to false
	Response  ApodResponse
	Responses []ApodResponse
}

// New returns an Apod with a default API key (falls back to $NASA_API_KEY,
// then "DEMO_KEY") and Date, DateRange, and Count left at their zero values.
// Set exactly one of them (e.g. a.Date.Set("2023-07-20")) and call Fetch on
// that field. Use NewApod instead when you already have a specific date,
// date range, or count to validate up front.
func New() *Apod {
	apiKey := &ApodAPIKey{}
	apiKey.Set("")

	return &Apod{
		APIKey: apiKey,
	}
}

// NewApod validates the given combination of date, date range, and count
// parameters (which are mutually exclusive) and returns a configured Apod.
func NewApod(apiKey *ApodAPIKey, date ApodDate, dateRange ApodDateRange, count ApodCount, thumbs bool) *Apod {
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

	return &Apod{
		APIKey:    apiKey,
		Date:      date,
		DateRange: dateRange,
		Count:     count,
		Thumbs:    thumbs,
	}
}

// composeQuery creates a query from a struct by marshalling it to json
type apodQuery struct {
	date      *ApodDate
	dateRange *ApodDateRange
	count     *ApodCount
}

func (a *Apod) composeQuery(q apodQuery) (*http.Request, error) {
	req, err := http.NewRequest("GET", BaseURL, nil)
	if err != nil {
		return nil, err
	}
	query := req.URL.Query()
	query.Add("api_key", a.APIKey.Key)

	switch {
	case q.date != nil:
		query.Add("date", q.date.String())
	case q.dateRange != nil:
		query.Add("start_date", q.dateRange.StartDate.String())
		query.Add("end_date", q.dateRange.EndDate.String())
	case q.count != nil:
		query.Add("count", q.count.String())
	default:
		query.Add("date", getTodayDate().String())
	}
	if a.Thumbs {
		query.Add("thumbs", "true")
	}
	req.URL.RawQuery = query.Encode()
	return req, nil
}

func (a *Apod) doFetch(q apodQuery) ([]ApodResponse, error) {
	req, err := a.composeQuery(q)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	a.APIKey.UpdateRateLimitInfo(resp) // now needs a lock — see below

	if a.APIKey.RateLimitExceeded() { // ditto
		return nil, errors.New("API rate limit exceeded, try again later")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status code: %d", resp.StatusCode)
	}

	reader, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return unwrap(reader) // free function now, returns instead of writing to a.Response/a.Responses
}

// unwrap takes the json object received from the API call and unwraps it to an array of ApodResponse structs
func unwrap(resp []byte) ([]ApodResponse, error) {
	var single ApodResponse
	if err := json.Unmarshal(resp, &single); err == nil {
		return []ApodResponse{single}, nil
	}

	var multiple []ApodResponse
	if err := json.Unmarshal(resp, &multiple); err != nil {
		return nil, err
	}
	return multiple, nil
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

// Set parses and sets the date value
func (d *ApodDate) Set(value string) error {
	// Validate the date format
	if value == "today" {
		*d = getTodayDate()
		return nil
	}

	t, err := time.Parse(DateFormat, value)
	if err != nil {
		return fmt.Errorf("invalid date format: %s, expected YYYY-MM-DD", value)
	}

	if t.Before(firstApodDate().Time) {
		return fmt.Errorf("date cannot be before %s", FirstDate)
	}

	d.Time = t
	return nil
}

// Fetch retrieves the APOD for this single date.
func (d *ApodDate) Fetch(a *Apod) ([]ApodResponse, error) {
	return a.doFetch(apodQuery{date: d})
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

// Fetch retrieves the APOD for every date in the range in a single API
// call. For ranges longer than MaximumApodRange, use FetchinBatches
// instead.
func (dr *ApodDateRange) Fetch(a *Apod) ([]ApodResponse, error) {
	return a.doFetch(apodQuery{dateRange: dr})
}

// FetchinBatches fetches a date range too large for a single API call by
// splitting it into chunks of batchSize days (each chunk is one API call,
// clamped to at most MaximumApodRange) and fetching up to concurrent
// chunks at a time. A response isn't visible on the returned channel until
// its whole chunk's API call completes, so a smaller batchSize trades more
// total requests for more frequent, incremental results instead of long
// silences while a large chunk is in flight. Responses and errors are
// delivered on the returned channels as they complete; both channels are
// closed once every chunk has been processed.
func (dr *ApodDateRange) FetchinBatches(ctx context.Context, a *Apod, concurrent int, batchSize int) (<-chan ApodResponse, <-chan error) {
	if batchSize <= 0 || batchSize > MaximumApodRange {
		batchSize = MaximumApodRange
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

			resps, err := a.doFetch(apodQuery{dateRange: &chunk})
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

// Fetch retrieves Count randomly chosen APODs.
func (c *ApodCount) Fetch(a *Apod) ([]ApodResponse, error) {
	return a.doFetch(apodQuery{count: c})
}

// ApodAPIKey is a custom type for handling API key parsing and validation
type ApodAPIKey struct {
	Key                string // Key is the API key sent to NASA's API, defaults to "DEMO_KEY"
	RateLimit          int    // RateLimit is the key's hourly request limit, from the X-RateLimit-Limit header
	RateLimitRemaining int    // RateLimitRemaining is the requests left this hour, from the X-RateLimit-Remaining header; -1 until known

	mu sync.Mutex // Mutex to protect concurrent access to RateLimitRemaining
}

// String returns the string representation of the API key
func (k *ApodAPIKey) String() string {
	return string(k.Key)
}

// Set sets the API key value and initializes the rate limit information
func (k *ApodAPIKey) Set(value string) error {
	var rateLimit int
	if value == "" {
		// Fall back to the environment variable; goapod defaults to "DEMO_KEY"
		// if the key is still empty.
		envValue, exists := os.LookupEnv("NASA_API_KEY")
		if !exists || envValue == "" {
			envValue = "DEMO_KEY"
		}
		value = envValue

		rateLimit = DemoKeyLimit

	} else {
		rateLimit = APIKeyLimit
	}

	k.Key = value
	k.RateLimitRemaining = -1 // Unknown rate limit remaining
	k.RateLimit = rateLimit

	return nil
}

// UpdateRateLimitInfo updates RateLimit and RateLimitRemaining from the
// X-RateLimit-Limit and X-RateLimit-Remaining response headers.
func (k *ApodAPIKey) UpdateRateLimitInfo(resp *http.Response) {
	if resp == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if limit := resp.Header.Get("X-RateLimit-Limit"); limit != "" {
		if n, err := strconv.Atoi(limit); err == nil {
			k.RateLimit = n
		}
	}

	if remaining := resp.Header.Get("X-RateLimit-Remaining"); remaining != "" {
		if n, err := strconv.Atoi(remaining); err == nil {
			k.RateLimitRemaining = n
		}
	}
}

// RateLimitExceeded checks if the rate limit has been exceeded
func (k *ApodAPIKey) RateLimitExceeded() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.RateLimitRemaining == 0
}

// ApodResponse holds the response JSON object received by a successful call to the APOD API
type ApodResponse struct {
	Date           string `json:"date"`
	Explanation    string `json:"explanation"`
	Hdurl          string `json:"hdurl"`
	MediaType      string `json:"media_type"`
	ServiceVersion string `json:"service_version"`
	Title          string `json:"title"`
	URL            string `json:"url"`
}

// String returns a human-readable summary of the APOD response.
func (a *ApodResponse) String() string {
	return fmt.Sprintf("Date: %s\nTitle: %s\nExplanation: %s\nMedia Type: %s\nURL: %s\nHD URL: %s\nService Version: %s\n",
		a.Date, a.Title, a.Explanation, a.MediaType, a.URL, a.Hdurl, a.ServiceVersion)
}

// FetchImage downloads the Apod Image in either hd or normal definition
// if hdurl is set but not available the function will default to url
func (a *ApodResponse) FetchImage(hdurl bool) ([]byte, error) {
	var src string
	if a.MediaType != "image" {
		return nil, errors.New("apodResponse is not an image")
	}

	if a.Hdurl != "" && hdurl {
		src = a.Hdurl
	} else {
		src = a.URL
	}

	// make request to src to fetch the file
	resp, err := http.Get(src)
	if err != nil {
		return []byte{}, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	return body, err
}
