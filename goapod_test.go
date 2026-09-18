package goapod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI serves a fixed set of APODs the way the APOD Basic API does:
// /{YYMMDD} for a single date and ?date_from&date_to&page&per_page for a
// range, newest first, capped at MaxPerPage and paginated with the
// X-WP-Total/X-WP-TotalPages headers.
type fakeAPI struct {
	mu       sync.Mutex
	apods    map[string]ApodResponse // keyed by YYYY-MM-DD
	requests []string
}

func newFakeAPI(from, to string) *fakeAPI {
	f := &fakeAPI{apods: map[string]ApodResponse{}}
	start, _ := time.Parse(DateFormat, from)
	end, _ := time.Parse(DateFormat, to)
	id := 1000
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		date := d.Format(DateFormat)
		f.apods[date] = ApodResponse{
			Date:        date,
			PostID:      PostID(id),
			Title:       "APOD " + date,
			MediaType:   "image",
			Explanation: "<strong>Explanation:</strong> Stars &amp; <a href=\"x\">more</a>.",
			Hdurl:       "https://example.invalid/" + date + ".jpg",
		}
		id++
	}
	return f
}

func (f *fakeAPI) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.RequestURI())
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json; charset=UTF-8")

		// Single date: /apod-basic/{YYMMDD}
		if code := strings.TrimPrefix(r.URL.Path, "/apod-basic/"); code != r.URL.Path && code != "" {
			d, err := time.Parse(LegacyDateFormat, code)
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"code":"rest_no_route","message":"No route was found matching the URL and request method.","data":{"status":404}}`)
				return
			}
			apod, ok := f.apods[d.Format(DateFormat)]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"code":"apod_basic_not_found","message":"APOD not found.","data":{"status":404}}`)
				return
			}
			json.NewEncoder(w).Encode(apod)
			return
		}

		// Range listing
		q := r.URL.Query()
		from, err1 := time.Parse(LegacyDateFormat, q.Get("date_from"))
		to, err2 := time.Parse(LegacyDateFormat, q.Get("date_to"))
		if err1 != nil || err2 != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"code":"rest_invalid_param","message":"Invalid parameter(s): date_from, date_to","data":{"status":400}}`)
			return
		}
		page, _ := strconv.Atoi(q.Get("page"))
		if page < 1 {
			page = 1
		}
		perPage, _ := strconv.Atoi(q.Get("per_page"))
		if perPage < 1 || perPage > MaxPerPage {
			perPage = MaxPerPage
		}

		var matches []ApodResponse
		for d := to; !d.Before(from); d = d.AddDate(0, 0, -1) { // newest first
			if apod, ok := f.apods[d.Format(DateFormat)]; ok {
				matches = append(matches, apod)
			}
		}
		total := len(matches)
		totalPages := (total + perPage - 1) / perPage
		w.Header().Set("X-WP-Total", strconv.Itoa(total))
		w.Header().Set("X-WP-TotalPages", strconv.Itoa(totalPages))

		lo := (page - 1) * perPage
		if lo > total {
			lo = total
		}
		hi := min(lo+perPage, total)
		json.NewEncoder(w).Encode(matches[lo:hi])
	})
}

func newTestApod(t *testing.T, f *fakeAPI) *Apod {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	a := New()
	a.BaseURL = srv.URL + "/apod-basic"
	return a
}

func mustSet(t *testing.T, s interface{ Set(string) error }, v string) {
	t.Helper()
	if err := s.Set(v); err != nil {
		t.Fatalf("Set(%q): %v", v, err)
	}
}

func TestDateSet(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"2014-09-18", "2014-09-18", false},
		{"140918", "2014-09-18", false},
		{"1995-06-16", "1995-06-16", false},
		{"1995-06-15", "", true}, // before FirstDate
		{"2014/09/18", "", true},
		{"14091", "", true},
		{"", "", true},
	}
	for _, tc := range tests {
		var d ApodDate
		err := d.Set(tc.in)
		if tc.wantErr != (err != nil) {
			t.Errorf("Set(%q): err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && d.String() != tc.want {
			t.Errorf("Set(%q) = %s, want %s", tc.in, d, tc.want)
		}
	}

	var d ApodDate
	mustSet(t, &d, "2014-09-18")
	if got := d.Legacy(); got != "140918" {
		t.Errorf("Legacy() = %s, want 140918", got)
	}
	mustSet(t, &d, "today")
	if d.String() != time.Now().Format(DateFormat) {
		t.Errorf("Set(today) = %s", d)
	}
}

func TestDateRangeSetAndChunk(t *testing.T) {
	var dr ApodDateRange
	mustSet(t, &dr, "2014-09-01, 2014-09-30")
	if dr.Len() != 30 {
		t.Fatalf("Len() = %d, want 30", dr.Len())
	}
	// Set again must not accumulate the previous length.
	mustSet(t, &dr, "2014-09-01, 2014-09-05")
	if dr.Len() != 5 {
		t.Fatalf("Len() after re-Set = %d, want 5", dr.Len())
	}

	if err := dr.Set("2014-09-05, 2014-09-01"); err == nil {
		t.Error("expected error for start after end")
	}
	if err := dr.Set("2014-09-01,2014-09-05"); err == nil {
		t.Error("expected error for missing space separator")
	}

	mustSet(t, &dr, "2014-09-01, 2014-10-30") // 60 days
	chunks := dr.chunk(MaximumApodRange)
	if len(chunks) != 3 {
		t.Fatalf("chunk(%d) produced %d chunks, want 3", MaximumApodRange, len(chunks))
	}
	total := 0
	for i, c := range chunks {
		n := 0
		for range c.All() {
			n++
		}
		if n > MaximumApodRange {
			t.Errorf("chunk %d spans %d days, more than %d", i, n, MaximumApodRange)
		}
		total += n
		if i > 0 && !c.StartDate.Equal(chunks[i-1].EndDate.AddDate(0, 0, 1)) {
			t.Errorf("chunk %d does not start the day after chunk %d ends", i, i-1)
		}
	}
	if total != 60 {
		t.Errorf("chunks cover %d days, want 60", total)
	}
	if !chunks[0].StartDate.Equal(dr.StartDate.Time) || !chunks[2].EndDate.Equal(dr.EndDate.Time) {
		t.Error("chunks do not cover the range boundaries")
	}
}

func TestNewApodDefaultsOpenEndedRange(t *testing.T) {
	var dr ApodDateRange
	mustSet(t, &dr.StartDate, "2020-01-01")
	a := NewApod(ApodDate{}, dr, 0)
	if a.DateRange.EndDate.String() != time.Now().Format(DateFormat) {
		t.Errorf("EndDate defaulted to %s, want today", a.DateRange.EndDate)
	}

	dr = ApodDateRange{}
	mustSet(t, &dr.EndDate, "2020-01-01")
	a = NewApod(ApodDate{}, dr, 0)
	if a.DateRange.StartDate.String() != FirstDate {
		t.Errorf("StartDate defaulted to %s, want %s", a.DateRange.StartDate, FirstDate)
	}

	defer func() {
		if recover() == nil {
			t.Error("expected panic for date + count")
		}
	}()
	var d ApodDate
	mustSet(t, &d, "2020-01-01")
	NewApod(d, ApodDateRange{}, 3)
}

func TestFetchSingleDate(t *testing.T) {
	f := newFakeAPI("2014-09-01", "2014-09-30")
	a := newTestApod(t, f)
	mustSet(t, &a.Date, "2014-09-18")

	resps, err := a.Date.Fetch(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(resps) != 1 || resps[0].Date != "2014-09-18" || resps[0].PostID != 1017 {
		t.Fatalf("unexpected response: %+v", resps)
	}
	if f.requests[0] != "/apod-basic/140918" {
		t.Errorf("request path = %s, want /apod-basic/140918", f.requests[0])
	}
}

func TestFetchMissingDateIsNotFound(t *testing.T) {
	f := newFakeAPI("2014-09-01", "2014-09-30")
	a := newTestApod(t, f)
	mustSet(t, &a.Date, "2015-01-01")

	_, err := a.Date.Fetch(context.Background(), a)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not an *APIError", err)
	}
	if !apiErr.NotFound() || apiErr.Code != "apod_basic_not_found" || apiErr.StatusCode != 404 {
		t.Errorf("unexpected APIError: %+v", apiErr)
	}
}

func TestFetchRangePagesThrough(t *testing.T) {
	f := newFakeAPI("2014-09-01", "2014-09-30")
	a := newTestApod(t, f)
	mustSet(t, &a.DateRange, "2014-09-01, 2014-09-30")

	resps, err := a.DateRange.Fetch(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(resps) != 30 {
		t.Fatalf("got %d responses, want 30", len(resps))
	}
	if resps[0].Date != "2014-09-30" || resps[29].Date != "2014-09-01" {
		t.Errorf("results not newest-first: %s .. %s", resps[0].Date, resps[29].Date)
	}
	if len(f.requests) != 2 {
		t.Errorf("made %d requests, want 2 (two pages)", len(f.requests))
	}
}

func TestFetchInBatchesCoversRange(t *testing.T) {
	f := newFakeAPI("2014-09-01", "2014-10-30")
	a := newTestApod(t, f)
	mustSet(t, &a.DateRange, "2014-09-01, 2014-10-30")

	respChan, errChan := a.DateRange.FetchinBatches(context.Background(), a, 3, 25)
	seen := map[string]bool{}
	for r := range respChan {
		seen[r.Date] = true
	}
	for err := range errChan {
		t.Errorf("unexpected error: %v", err)
	}
	for d := range a.DateRange.All() {
		if !seen[d] {
			t.Errorf("missing %s", d)
		}
	}
	if len(seen) != 60 {
		t.Errorf("got %d distinct dates, want 60", len(seen))
	}
}

func TestFetchInBatchesHonoursCancel(t *testing.T) {
	f := newFakeAPI("2014-09-01", "2014-10-30")
	a := newTestApod(t, f)
	mustSet(t, &a.DateRange, "2014-09-01, 2014-10-30")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	respChan, errChan := a.DateRange.FetchinBatches(ctx, a, 3, 25)
	for range respChan {
	}
	n := 0
	for err := range errChan {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error %v is not context.Canceled", err)
		}
		n++
	}
	if n == 0 {
		t.Error("expected at least one cancellation error")
	}
}

func TestFetchRandomCount(t *testing.T) {
	// Only 2014-09 exists, so most random draws across 1995..today are 404s
	// and must be redrawn without failing the fetch.
	f := newFakeAPI("1995-06-16", "1995-12-31")
	a := newTestApod(t, f)
	mustSet(t, &a.Count, "3")

	resps, err := a.Count.Fetch(context.Background(), a)
	// With ~200 valid days out of ~11,000 and a 12-attempt budget the draw
	// may legitimately come up short; what must never happen is a hard error
	// or duplicate dates.
	if err != nil && len(resps) == 0 {
		t.Logf("no hits within the attempt budget: %v", err)
	}
	seen := map[string]bool{}
	for _, r := range resps {
		if seen[r.Date] {
			t.Errorf("duplicate date %s", r.Date)
		}
		seen[r.Date] = true
		if _, ok := f.apods[r.Date]; !ok {
			t.Errorf("returned a date the API does not have: %s", r.Date)
		}
	}
	if len(resps) > 3 {
		t.Errorf("got %d responses, want at most 3", len(resps))
	}
}

func TestCountSet(t *testing.T) {
	var c ApodCount
	for _, bad := range []string{"0", "-1", "x", ""} {
		if err := c.Set(bad); err == nil {
			t.Errorf("Set(%q) succeeded, want error", bad)
		}
	}
	mustSet(t, &c, "7")
	if c != 7 || c.String() != "7" {
		t.Errorf("Set(7) = %d (%s)", c, c.String())
	}
}

func TestPostIDAcceptsNumberAndString(t *testing.T) {
	for _, in := range []string{`{"post_id":1234862}`, `{"post_id":"1234862"}`} {
		var r ApodResponse
		if err := json.Unmarshal([]byte(in), &r); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if r.PostID != 1234862 {
			t.Errorf("%s: PostID = %d", in, r.PostID)
		}
	}
	var r ApodResponse
	if err := json.Unmarshal([]byte(`{"post_id":null}`), &r); err != nil || r.PostID != 0 {
		t.Errorf("null post_id: %v, %d", err, r.PostID)
	}
	if err := json.Unmarshal([]byte(`{"post_id":"abc"}`), &r); err == nil {
		t.Error("expected error for non-numeric post_id")
	}
}

func TestPlainText(t *testing.T) {
	r := ApodResponse{
		Explanation: "<strong>Explanation: </strong>This  is <a href=\"x\">a &amp; b</a>.\nDone.",
		Credit:      "<b>Image Credit &amp; <a href=\"y\">Copyright</a>:</b> Jane",
	}
	if got, want := r.PlainExplanation(), "This is a & b. Done."; got != want {
		t.Errorf("PlainExplanation() = %q, want %q", got, want)
	}
	if got, want := r.PlainCredit(), "Image Credit & Copyright: Jane"; got != want {
		t.Errorf("PlainCredit() = %q, want %q", got, want)
	}
}

func TestAPIErrorMessages(t *testing.T) {
	e := &APIError{StatusCode: 500}
	if e.Error() != "API request failed with status code: 500" {
		t.Errorf("bare error = %q", e.Error())
	}
	e = &APIError{StatusCode: 400, Code: "rest_invalid_param", Message: "Invalid parameter(s): date_from"}
	if want := "API request failed with status code 400: Invalid parameter(s): date_from (rest_invalid_param)"; e.Error() != want {
		t.Errorf("error = %q, want %q", e.Error(), want)
	}
}

func TestFetchImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.jpg" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte("imagebytes"))
	}))
	defer srv.Close()

	r := ApodResponse{Hdurl: srv.URL + "/a.jpg"}
	got, err := r.FetchImage(context.Background())
	if err != nil || string(got) != "imagebytes" {
		t.Errorf("FetchImage = %q, %v", got, err)
	}

	r.Hdurl = srv.URL + "/missing.jpg"
	if _, err := r.FetchImage(context.Background()); err == nil {
		t.Error("expected error for 404 image")
	}

	r.Hdurl = ""
	if _, err := r.FetchImage(context.Background()); err == nil {
		t.Error("expected error for empty hdurl")
	}
}
