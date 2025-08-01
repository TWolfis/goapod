package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/TWolfis/goapod"
	_ "github.com/lib/pq" // Import PostgreSQL driver for CockroachDB
	"gopkg.in/yaml.v3"
)

const (
	FIRST_DATE = "1995-06-16" // First date of APOD
)

// Custom usage function for better help formatting
func init() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `afetch - NASA Astronomy Picture of the Day (APOD) Fetcher

USAGE:
    %s [FLAGS]

FLAGS:
`, os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), `
EXAMPLES:
    %s -d 2023-07-20                    # Get APOD for a specific date
    %s -sd 2023-07-01 -ed 2023-07-07    # Get APOD range
    %s -d 2023-07-20 -dl -hd            # Download HD image for specific date
    %s --save-yaml --yaml-file config.yaml  # Save current options to YAML
    %s --read-yaml --yaml-file config.yaml  # Read options from YAML

ENVIRONMENT VARIABLES:
    COCKROACH_PASSWORD         CockroachDB database password (alternative to --cockroach-pass flag)
    NASA_API_KEY               NASA API key for APOD service (alternative to --api-key flag)

`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
	}

	// Define all flags
	flag.BoolVar(&opts.Download, "dl", false, "download image")
	flag.StringVar(&opts.DstFile, "df", "", "destination file for downloaded image")
	flag.BoolVar(&opts.Hdurl, "hd", false, "use hd image url")
	flag.StringVar(&opts.Date, "d", "", "date for APOD in format YYYY-MM-DD")
	flag.StringVar(&opts.Startdate, "sd", "", "start date for APOD range in format YYYY-MM-DD")
	flag.StringVar(&opts.Enddate, "ed", "", "end date for APOD range in format YYYY-MM-DD")
	flag.StringVar(&opts.ApiKey, "api-key", "", "NASA API key for APOD service")

	flag.BoolVar(&opts.Cockroach, "cockroach", false, "save to CockroachDB database")
	flag.StringVar(&opts.CockroachUser, "cockroach-user", "root", "CockroachDB username")
	flag.StringVar(&opts.CockroachPassword, "cockroach-pass", "", "CockroachDB password")
	flag.StringVar(&opts.CockroachHost, "cockroach-host", "localhost", "CockroachDB hostname")
	flag.StringVar(&opts.CockroachPort, "cockroach-port", "26257", "CockroachDB port")
	flag.StringVar(&opts.CockroachDatabase, "cockroach-db", "nasa", "CockroachDB database name")
	flag.StringVar(&opts.CockroachTable, "cockroach-table", "apod", "CockroachDB table name")

	flag.BoolVar(&everyting, "everything", false, "fetch everything")
	flag.BoolVar(&saveToYaml, "save-yaml", false, "save options to YAML file")
	flag.BoolVar(&readFromYaml, "read-yaml", false, "read options from YAML file")
	flag.StringVar(&yamlFile, "yaml-file", "apod_options.yaml", "YAML file path for saving/reading options")
	flag.BoolVar(&help, "h", false, "show help message")
}

// Global variables for flags
var (
	opts         Options
	everyting    bool
	saveToYaml   bool
	readFromYaml bool
	yamlFile     string
	help         bool
)

type Options struct {
	Date      string `yaml:"date"`
	Startdate string `yaml:"startdate"`
	Enddate   string `yaml:"enddate"`
	Download  bool   `yaml:"download"`
	DstFile   string `yaml:"dstFile"`
	Hdurl     bool   `yaml:"hdurl"`
	ApiKey    string `yaml:"apiKey"`

	Cockroach         bool   `yaml:"cockroach"`
	CockroachPassword string `yaml:"cockroachPassword"`
	CockroachDatabase string `yaml:"cockroachDatabase"`
	CockroachTable    string `yaml:"cockroachTable"`
	CockroachUser     string `yaml:"cockroachUser"`
	CockroachHost     string `yaml:"cockroachHost"`
	CockroachPort     string `yaml:"cockroachPort"`
}

func SetOptionsFromFile(filePath string) (*Options, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading options file: %w", err)
	}

	var opts Options
	err = yaml.Unmarshal(data, &opts)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling options from YAML: %w", err)
	}

	return &opts, nil
}

func (opt *Options) ToYaml(dst string) error {
	data, err := yaml.Marshal(opt)
	if err != nil {
		return fmt.Errorf("error marshalling options to YAML: %w", err)
	}

	err = os.WriteFile(dst, data, 0644)
	if err != nil {
		return fmt.Errorf("error writing options to file: %w", err)
	}

	return nil
}

func Everything(opts *Options, a *goapod.Apod) error {
	opts.Startdate = FIRST_DATE
	today := time.Now().Format("2006-01-02")
	opts.Enddate = today

	daysleft := time.Since(time.Date(1995, 6, 16, 0, 0, 0, 0, time.UTC)).Hours() / 24

	// iterate over each week from FIRST_DATE to today
	for i := 0; i <= int(daysleft); i += 7 {
		startDate := time.Date(1995, 6, 16, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format("2006-01-02")
		endDate := time.Date(1995, 6, 16, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i+6).Format("2006-01-02")

		a.StartDate = startDate
		a.EndDate = endDate

		err := a.Fetch()
		if err != nil {
			return fmt.Errorf("error fetching APOD for range %s to %s: %w", startDate, endDate, err)
		}

		if len(a.Responses) > 0 {
			fmt.Printf("Fetched APOD from %s to %s\n", startDate, endDate)
			PrintApod(a)
		}

		if opts.Download {
			err = SaveImage(a, opts.DstFile, opts.Hdurl)
			if err != nil {
				return fmt.Errorf("error downloading image for range %s to %s: %w", startDate, endDate, err)
			}
		}
		if opts.Cockroach {
			err := SaveToCockroach(a, opts)
			if err != nil {
				return fmt.Errorf("error saving to CockroachDB for range %s to %s: %w", startDate, endDate, err)
			}
		}
		fmt.Printf("APOD for range %s to %s processed successfully.\n", startDate, endDate)
	}
	return nil
}

func PrintApod(a *goapod.Apod) {
	if len(a.Responses) == 0 {
		ar := a.Response
		fmt.Printf("Title: %s\nDate: %s\nExplanation: %s\nURL: %s\n",
			ar.Title, ar.Date, ar.Explanation, ar.URL)
	} else if len(a.Responses) > 1 {
		for _, ar := range a.Responses {
			fmt.Printf("Title: %s\nDate: %s\nExplanation: %s\nURL: %s\n\n",
				ar.Title, ar.Date, ar.Explanation, ar.URL)
		}
	} else {
		fmt.Println("No APOD for this date")
	}
}

func SaveImage(a *goapod.Apod, dstFile string, hdurl bool) error {
	if len(a.Responses) == 0 {
		ar := a.Response
		if dstFile == "" {
			dstFile = strings.ToLower(ar.Title) + ".jpg"
			img, err := ar.FetchImage(hdurl)
			if err != nil {
				return err
			}
			os.WriteFile(dstFile, img, 0644)
		}
	} else if len(a.Responses) > 1 {
		for _, ar := range a.Responses {
			dstFile = strings.ToLower(ar.Title) + ".jpg"
			img, err := ar.FetchImage(hdurl)
			if err != nil {
				return err
			}
			os.WriteFile(dstFile, img, 0644)
		}
	} else {
		return fmt.Errorf("no APOD for this date")
	}

	return nil
}

func SaveToCockroach(a *goapod.Apod, opts *Options) error {
	if opts.CockroachPassword == "" {
		opts.CockroachPassword = os.Getenv("COCKROACH_PASSWORD")

		if opts.CockroachPassword == "" {
			fmt.Println("CockroachDB password not set. Please set the COCKROACH_PASSWORD environment variable or flag.")
			return fmt.Errorf("CockroachDB password not set")
		}
	}

	// Create CockroachDB connection string (PostgreSQL format)
	dsn := fmt.Sprintf("postgresql://%s:%s@%s:%s/%s?sslmode=require",
		opts.CockroachUser, opts.CockroachPassword, opts.CockroachHost, opts.CockroachPort, opts.CockroachDatabase)

	if len(a.Responses) == 0 {
		return a.Response.SaveToCockroach(dsn, opts.CockroachTable)
	} else if len(a.Responses) > 1 {
		for _, ar := range a.Responses {
			err := ar.SaveToCockroach(dsn, opts.CockroachTable)
			if err != nil {
				return err
			}
		}
	} else {
		return fmt.Errorf("no APOD for this date")
	}

	return nil
}

func main() {
	flag.Parse()

	if help {
		flag.Usage()
		os.Exit(0)
	}

	var err error

	if readFromYaml {
		optsFromFile, err := SetOptionsFromFile(yamlFile)
		if err != nil {
			fmt.Printf("Error reading from YAML file: %v\n", err)
			os.Exit(1)
		}
		opts = *optsFromFile
	}

	a := goapod.Apod{}

	// Set API key from flag or environment variable
	if opts.ApiKey != "" {
		a.ApiKey = opts.ApiKey
	} else if envApiKey := os.Getenv("NASA_API_KEY"); envApiKey != "" {
		a.ApiKey = envApiKey
	}
	// If no API key is provided, the goapod package will use "DEMO_KEY" by default

	if everyting {
		err = Everything(&opts, &a)
		if err != nil {
			fmt.Printf("Error fetching everything: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if opts.Date != "" {
		a.Date = opts.Date
	} else if opts.Startdate != "" && opts.Enddate != "" {
		a.StartDate = opts.Startdate
		a.EndDate = opts.Enddate
	}

	err = a.Fetch()
	if err != nil {
		fmt.Printf("Error fetching APOD: %v\n", err)
		os.Exit(1)
	}

	PrintApod(&a)

	// download image
	if opts.Download {
		err = SaveImage(&a, opts.DstFile, opts.Hdurl)
		if err != nil {
			fmt.Printf("Error downloading image: %v\n", err)
			os.Exit(1)
		}
	}

	// Save to YAML if requested
	if saveToYaml {
		err = opts.ToYaml(yamlFile)
		if err != nil {
			fmt.Printf("Error saving to YAML file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Options saved to %s\n", yamlFile)
	}

	// save to CockroachDB
	if opts.Cockroach {
		err := SaveToCockroach(&a, &opts)
		if err != nil {
			fmt.Println("Error saving to CockroachDB:", err)
			os.Exit(1)
		}
		fmt.Println("APOD saved to CockroachDB successfully.")
	}

}
