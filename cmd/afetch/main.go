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

type Options struct {
	date      string `yaml:"date"`
	startdate string `yaml:"startdate"`
	enddate   string `yaml:"enddate"`
	download  bool   `yaml:"download"`
	dstFile   string `yaml:"dstFile"`
	hdurl     bool   `yaml:"hdurl"`

	db2         bool   `yaml:"db2"`
	db2Password string `yaml:"db2Password"`
	db2Database string `yaml:"db2Database"`
	db2Table    string `yaml:"db2Table"`
	db2User     string `yaml:"db2User"`
}

func SetOptions() (*Options, error) {
	opts := &Options{}

	flag.StringVar(&opts.date, "d", "", "date for APOD in format YYYY-MM-DD")
	flag.StringVar(&opts.startdate, "sd", "", "start date for APOD range in format YYYY-MM-DD")
	flag.StringVar(&opts.enddate, "ed", "", "end date for APOD range in format YYYY-MM-DD")
	flag.BoolVar(&opts.download, "dl", false, "download image")
	flag.StringVar(&opts.dstFile, "df", "", "destination file for downloaded image")
	flag.BoolVar(&opts.hdurl, "hd", false, "use hd image url")

	flag.BoolVar(&opts.db2, "db2", false, "save to DB2 database")
	flag.StringVar(&opts.db2User, "db2user", "db2inst1", "DB2 username")
	flag.StringVar(&opts.db2Password, "db2pass", "", "DB2 password")
	flag.StringVar(&opts.db2Database, "db2db", "nasa", "DB2 database name")
	flag.StringVar(&opts.db2Table, "db2table", "apod", "DB2 table name")

	flag.Parse()

	return opts, nil
}

func SetOptionsWithYamlFlags() (*Options, *bool, *bool, *string, error) {
	opts := &Options{}
	saveToYaml := flag.Bool("save-yaml", false, "save options to YAML file")
	readFromYaml := flag.Bool("read-yaml", false, "read options from YAML file")
	yamlFile := flag.String("yaml-file", "apod_options.yaml", "YAML file path for saving/reading options")

	flag.StringVar(&opts.date, "d", "", "date for APOD in format YYYY-MM-DD")
	flag.StringVar(&opts.startdate, "sd", "", "start date for APOD range in format YYYY-MM-DD")
	flag.StringVar(&opts.enddate, "ed", "", "end date for APOD range in format YYYY-MM-DD")
	flag.BoolVar(&opts.download, "dl", false, "download image")
	flag.StringVar(&opts.dstFile, "df", "", "destination file for downloaded image")
	flag.BoolVar(&opts.hdurl, "hd", false, "use hd image url")

	flag.BoolVar(&opts.db2, "db2", false, "save to DB2 database")
	flag.StringVar(&opts.db2User, "db2user", "db2inst1", "DB2 username")
	flag.StringVar(&opts.db2Password, "db2pass", "", "DB2 password")
	flag.StringVar(&opts.db2Database, "db2db", "nasa", "DB2 database name")
	flag.StringVar(&opts.db2Table, "db2table", "apod", "DB2 table name")

	flag.Parse()

	return opts, saveToYaml, readFromYaml, yamlFile, nil
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

func VerifyDate(date string) error {
	_, err := time.Parse("2006-1-1", date)
	return err
}

func PrintApod(a *goapod.Apod) {
	if len(a.Responses) == 0 {
		ar := a.Response
		fmt.Printf("Title: %s\nDate: %s\nExplanation: %s\nURL: %s",
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
	if opts.db2Password == "" {
		opts.db2Password = os.Getenv("DB2_PASSWORD")

		if opts.db2Password == "" {
			fmt.Println("DB2 password not set. Please set the DB2_PASSWORD environment variable or flag.")
			return fmt.Errorf("DB2 password not set")
		}
	}

	if len(a.Responses) == 0 {
		return a.Response.SaveToDb2(fmt.Sprintf("user=%s password=%s database=%s table=%s",
			opts.db2User, opts.db2Password, opts.db2Database, opts.db2Table))
	} else if len(a.Responses) > 1 {
		for _, ar := range a.Responses {
			err := ar.SaveToDb2(fmt.Sprintf("user=%s password=%s database=%s table=%s",
				opts.db2User, opts.db2Password, opts.db2Database, opts.db2Table))
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
	var opts *Options
	var err error
	var saveToYaml, readFromYaml bool
	var yamlFile string

	// First, check if we should read from YAML
	// We need to do a preliminary parse to check for read-yaml flag
	flag.BoolVar(&readFromYaml, "read-yaml", false, "read options from YAML file")
	flag.StringVar(&yamlFile, "yaml-file", "apod_options.yaml", "YAML file path for saving/reading options")
	flag.Parse()

	if readFromYaml {
		opts, err = SetOptionsFromFile(yamlFile)
		if err != nil {
			fmt.Printf("Error reading from YAML file: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Reset flag parsing and use SetOptionsWithYamlFlags
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
		var saveToYamlPtr, readFromYamlPtr *bool
		var yamlFilePtr *string
		opts, saveToYamlPtr, readFromYamlPtr, yamlFilePtr, err = SetOptionsWithYamlFlags()
		if err != nil {
			fmt.Printf("Error setting options: %v\n", err)
			os.Exit(1)
		}
		saveToYaml = *saveToYamlPtr
		readFromYaml = *readFromYamlPtr
		yamlFile = *yamlFilePtr
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

	a := goapod.Apod{}

	if opts.date != "" {
		err := VerifyDate(opts.date)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		a.Date = opts.date
	} else if opts.startdate != "" && opts.enddate != "" {
		err := VerifyDate(opts.startdate)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		a.StartDate = opts.startdate

		err = VerifyDate(opts.enddate)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		a.EndDate = opts.enddate
	}

	err = a.Fetch()
	if err != nil {
		fmt.Printf("Error fetching APOD: %v\n", err)
		os.Exit(1)
	}

	PrintApod(&a)

	// download image
	if opts.download {
		err = SaveImage(&a, opts.dstFile, opts.hdurl)
		if err != nil {
			fmt.Printf("Error downloading image: %v\n", err)
			os.Exit(1)
		}
	}

	// save to DB2
	if opts.db2 {
		err := SaveToDb2(&a, opts)
		if err != nil {
			fmt.Println("Error saving to DB2:", err)
			os.Exit(1)
		}
		fmt.Println("APOD saved to DB2 successfully.")
	}
}
