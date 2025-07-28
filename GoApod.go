package goapod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
)

// Apod contains the variables associated with the APOD API
type Apod struct {
	ApiKey    string // ApiKey is the users personal ApiKey defaults to "DEMO_KEY"
	Date      string `json:"date"`       // Date of the Apod image to retrieve defaults to today
	StartDate string `json:"start_date"` // StartDate of a date range, when requesting for a range of dates. Cannot be used with Date, defaults to none
	EndDate   string `json:"end_date"`   // EndDate The end of the date range, when used with StartDate, defaults to today
	Count     int    `json:"count"`      // Count If this is specified then count randomly chosen images will be returned. Cannot be used with date or StartDate and EndDate, defaults to none
	Thumbs    bool   `json:"thumbs"`     // Thumbs Return the URL of video thumbnail. If an Apod is not a video, this parameter is ignored, defaults to false
	Response  ApodResponse
	Responses []ApodResponse
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

// composeQuery creates a query from a struct by marshalling it to json
func (a *Apod) composeQuery() (*http.Request, error) {
	url := "https://api.nasa.gov/planetary/apod"

	//	create get request, returns request object
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return &http.Request{}, err
	}

	// query can be used to add query options
	query := req.URL.Query()

	// add api_key to query
	// if ApiKey is not set use the default "DEMO_KEY"
	if a.ApiKey == "" {
		a.ApiKey = "DEMO_KEY"
	}
	query.Add("api_key", a.ApiKey)

	// handle various possible query parameters
	switch {
	case a.Date != "" && a.StartDate == "" && a.EndDate == "" && a.Count == 0:
		// If date is set and StartDate, EndDate, and Count are not set
		query.Add("date", a.Date)

	case a.Date == "" && a.StartDate != "" && a.EndDate != "" && a.Count == 0:
		// If Date is not set, StartDate and EndDate are set, and Count is not set
		query.Add("start_date", a.StartDate)
		query.Add("end_date", a.EndDate)

	case a.Date == "" && a.StartDate == "" && a.EndDate == "" && a.Count > 0:
		// If Date, StartDate, and EndDate are not set, but Count is set
		query.Add("count", strconv.Itoa(a.Count))
	}

	if a.Thumbs {
		// If Thumbs is set add Thumbs to query
		query.Add("thumbs", "True")
	}
	req.URL.RawQuery = query.Encode()
	return req, nil
}

// unwrap takes the json object received from the API call and unwraps it to an array of ApodResponse structs
func (a *Apod) unwrap(resp []byte) error {

	//unmarshal the received json objects
	err := json.Unmarshal(resp, &a.Response)
	if err != nil {
		err = json.Unmarshal(resp, &a.Responses)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *Apod) Fetch() error {

	// Create request
	request, err := a.composeQuery()
	if err != nil {
		return err
	}

	// Create client and make request to the API
	client := &http.Client{}
	resp, err := client.Do(request)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	reader, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	err = a.unwrap(reader)

	return err
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

	//make request to src to fetch the file
	resp, err := http.Get(src)
	if err != nil {
		return []byte{}, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	return body, err
}

func (a *ApodResponse) SaveToDb2(connStr string) error {
	db, err := sql.Open("go_ibm_db", connStr)
	if err != nil {
		return err
	}
	defer db.Close()

	query := `
    INSERT INTO apod (
        apod_date, title, explanation, media_type, url, hdurl, service_version, copyright
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    `

	_, err = db.Exec(
		query,
		a.Date,
		a.Title,
		a.Explanation,
		a.MediaType,
		a.URL,
		a.Hdurl,
		a.ServiceVersion,
		sql.NullString{String: "", Valid: false}, // Copyright: optional
	)

	if err != nil {
		log.Printf("Failed to insert APOD %s: %v\n", a.Date, err)
	}

	return err
}

func (a *ApodResponse) SaveToMySQL(dsn, tableName string) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	// Test the connection
	if err = db.Ping(); err != nil {
		return err
	}

	query := fmt.Sprintf(`
    INSERT INTO %s (
        apod_date, title, explanation, media_type, url, hdurl, service_version, copyright
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    ON DUPLICATE KEY UPDATE
        title = VALUES(title),
        explanation = VALUES(explanation),
        media_type = VALUES(media_type),
        url = VALUES(url),
        hdurl = VALUES(hdurl),
        service_version = VALUES(service_version),
        copyright = VALUES(copyright)
    `, tableName)

	_, err = db.Exec(
		query,
		a.Date,
		a.Title,
		a.Explanation,
		a.MediaType,
		a.URL,
		a.Hdurl,
		a.ServiceVersion,
		sql.NullString{String: "", Valid: false}, // Copyright: optional
	)

	if err != nil {
		log.Printf("Failed to insert/update APOD %s: %v\n", a.Date, err)
	}

	return err
}
