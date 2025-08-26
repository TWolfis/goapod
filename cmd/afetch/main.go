package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/TWolfis/goapod"
	"gopkg.in/yaml.v3"
)

const (
	FIRST_DATE = "1995-06-16" // First date of APOD
)

// Database enum type
type Database int

const (
	MySQL Database = iota
	PostgreSQL
)

func (d Database) String() string {
	switch d {
	case MySQL:
		return "MySQL"
	case PostgreSQL:
		return "PostgreSQL"
	default:
		return "Unknown"
	}
}

// ParseDatabase converts string → Database
func ParseDatabase(s string) (Database, error) {
	switch strings.ToLower(s) {
	case "mysql":
		return MySQL, nil
	case "postgresql", "postgres", "pg":
		return PostgreSQL, nil
	default:
		return -1, fmt.Errorf("unsupported database: %s", s)
	}
}

// For integration with `flag`
type dbFlag struct{ db *Database }

func (f *dbFlag) String() string {
	if f.db == nil {
		return ""
	}
	return f.db.String()
}

func (f *dbFlag) Set(s string) error {
	parsed, err := ParseDatabase(s)
	if err != nil {
		return err
	}
	*f.db = parsed
	return nil
}

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
    MYSQL_PASSWORD             MySQL database password (alternative to --mysql-pass flag)
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

	flag.BoolVar(&opts.Db, "db", false, "save to database (MySQL or PostgreSQL)")
	flag.StringVar(&opts.DbType, "db-type", "mysql", "database type: mysql or postgresql")
	flag.StringVar(&opts.DbUser, "db-user", "root", "database username")
	flag.StringVar(&opts.DbPassword, "db-pass", "", "database password")
	flag.StringVar(&opts.DbHost, "db-host", "localhost", "database hostname")
	flag.StringVar(&opts.DbPort, "db-port", "3306", "database port")
	flag.StringVar(&opts.DbName, "db-name", "nasa", "database name")
	flag.StringVar(&opts.DbTable, "db-table", "apod", "database table name")

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

	Db         bool   `yaml:"db"`
	DbType     string `yaml:"dbType"`
	DbUser     string `yaml:"dbUser"`
	DbPassword string `yaml:"dbPassword"`
	DbHost     string `yaml:"dbHost"`
	DbPort     string `yaml:"dbPort"`
	DbName     string `yaml:"dbName"`
	DbTable    string `yaml:"dbTable"`
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

		if opts.Db {
			err := SaveToDB(a, opts)
			if err != nil {
				return fmt.Errorf("error saving to %s for range %s to %s: %w", opts.DbType, startDate, endDate, err)
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

func SaveToDB(a *goapod.Apod, opts *Options) error {
	// Set password from env if not provided
	if opts.DbPassword == "" {
		switch opts.DbType {
		case "mysql":
			opts.DbPassword = os.Getenv("MYSQL_PASSWORD")
		case "postgresql":
			opts.DbPassword = os.Getenv("POSTGRES_PASSWORD")
		}
	}

	var dsn string
	switch opts.DbType {
	case "mysql":
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s",
			opts.DbUser, opts.DbPassword, opts.DbHost, opts.DbPort, opts.DbName)
	case "postgresql":
		dsn = fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			opts.DbHost, opts.DbPort, opts.DbUser, opts.DbPassword, opts.DbName)
	default:
		return fmt.Errorf("unsupported database type: %s", opts.DbType)
	}

	if len(a.Responses) == 0 {
		switch opts.DbType {
		case "mysql":
			return a.Response.SaveToMySQL(dsn, opts.DbTable)
		case "postgresql":
			return a.Response.SaveToPostgreSQL(dsn, opts.DbTable)
		}
	} else if len(a.Responses) > 1 {
		for _, ar := range a.Responses {
			switch opts.DbType {
			case "mysql":
				err := ar.SaveToMySQL(dsn, opts.DbTable)
				if err != nil {
					return err
				}
			case "postgresql":
				err := ar.SaveToPostgreSQL(dsn, opts.DbTable)
				if err != nil {
					return err
				}
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

	// save to DB
	if opts.Db {
		err := SaveToDB(&a, &opts)
		if err != nil {
			fmt.Println("Error saving to database:", err)
			os.Exit(1)
		}
		fmt.Println("APOD saved to database successfully.")
	}

}
