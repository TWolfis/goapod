package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/TWolfis/goapod"
	_ "github.com/ibmdb/go_ibm_db" // Import DB2 driver for side effects
	"gopkg.in/yaml.v3"
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
    DB2_PASSWORD               DB2 database password (alternative to --db2pass flag)

`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
	}

	// Define all flags
	flag.BoolVar(&opts.Download, "dl", false, "download image")
	flag.StringVar(&opts.DstFile, "df", "", "destination file for downloaded image")
	flag.BoolVar(&opts.Hdurl, "hd", false, "use hd image url")
	flag.StringVar(&opts.Date, "d", "", "date for APOD in format YYYY-MM-DD")
	flag.StringVar(&opts.Startdate, "sd", "", "start date for APOD range in format YYYY-MM-DD")
	flag.StringVar(&opts.Enddate, "ed", "", "end date for APOD range in format YYYY-MM-DD")

	flag.BoolVar(&opts.Db2, "db2", false, "save to DB2 database")
	flag.StringVar(&opts.Db2User, "db2user", "db2inst1", "DB2 username")
	flag.StringVar(&opts.Db2Password, "db2pass", "", "DB2 password")
	flag.StringVar(&opts.Db2Database, "db2db", "nasa", "DB2 database name")
	flag.StringVar(&opts.Db2Table, "db2table", "apod", "DB2 table name")

	flag.BoolVar(&saveToYaml, "save-yaml", false, "save options to YAML file")
	flag.BoolVar(&readFromYaml, "read-yaml", false, "read options from YAML file")
	flag.StringVar(&yamlFile, "yaml-file", "apod_options.yaml", "YAML file path for saving/reading options")
	flag.BoolVar(&help, "h", false, "show help message")
}

// Global variables for flags
var (
	opts         Options
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

	Db2         bool   `yaml:"db2"`
	Db2Password string `yaml:"db2Password"`
	Db2Database string `yaml:"db2Database"`
	Db2Table    string `yaml:"db2Table"`
	Db2User     string `yaml:"db2User"`
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

func SaveToDb2(a *goapod.Apod, opts *Options) error {
	if opts.Db2Password == "" {
		opts.Db2Password = os.Getenv("DB2_PASSWORD")

		if opts.Db2Password == "" {
			fmt.Println("DB2 password not set. Please set the DB2_PASSWORD environment variable or flag.")
			return fmt.Errorf("DB2 password not set")
		}
	}

	if len(a.Responses) == 0 {
		return a.Response.SaveToDb2(fmt.Sprintf("user=%s password=%s database=%s table=%s",
			opts.Db2User, opts.Db2Password, opts.Db2Database, opts.Db2Table))
	} else if len(a.Responses) > 1 {
		for _, ar := range a.Responses {
			err := ar.SaveToDb2(fmt.Sprintf("user=%s password=%s database=%s table=%s",
				opts.Db2User, opts.Db2Password, opts.Db2Database, opts.Db2Table))
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

	// save to DB2
	if opts.Db2 {
		err := SaveToDb2(&a, &opts)
		if err != nil {
			fmt.Println("Error saving to DB2:", err)
			os.Exit(1)
		}
		fmt.Println("APOD saved to DB2 successfully.")
	}

}
