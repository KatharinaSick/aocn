package app

import (
	"errors"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// defaultServerPort is used when PORT is not set, which is the case locally.
// Cloud Run always sets it.
const defaultServerPort = "8080"

const defaultSiteURL = "https://aocn.dev"

type Config struct {
	// Dev turns on stand-ins for local development: emails are logged instead of sent
	// when there is no Resend key, and a random signing key is used when there is none.
	// Only `devbox run api` sets it. Without it, missing secrets stop the API at startup,
	// so a broken deploy cannot quietly accept signups that never send an email.
	Dev bool `yaml:"dev"`

	Server struct {
		Port string `yaml:"port"`
	} `yaml:"server"`

	Schedule struct {
		Path string `yaml:"path"`
	} `yaml:"schedule"`

	// Doors says where the challenges repository uploaded the doors: a GCS bucket on Cloud Run, a folder laid
	// out like it locally.
	Doors struct {
		Bucket string `yaml:"bucket"`
		Dir    string `yaml:"dir"`
	} `yaml:"doors"`

	Site struct {
		URL string `yaml:"url"`
	} `yaml:"site"`

	Database struct {
		URL string `yaml:"url"`
	} `yaml:"database"`

	Resend struct {
		APIKey      string `yaml:"apiKey"`
		FromAddress string `yaml:"fromAddress"`
		SegmentID   string `yaml:"segmentId"`
	} `yaml:"resend"`

	Subscribe struct {
		SigningKey string `yaml:"signingKey"`
	} `yaml:"subscribe"`
}

// ParseConfig reads ./config.yaml relative to the working directory, so the API must
// be started from the api/ directory. `devbox run api` does that; so does the
// container, which copies the file next to the binary.
func ParseConfig() (*Config, error) {
	data, err := os.ReadFile("./config.yaml")
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(data))), cfg); err != nil {
		return nil, err
	}

	if cfg.Server.Port == "" {
		cfg.Server.Port = defaultServerPort
	}
	if cfg.Site.URL == "" {
		cfg.Site.URL = defaultSiteURL
	}
	cfg.Site.URL = strings.TrimSuffix(cfg.Site.URL, "/")

	if err := cfg.validate(OnCloudRun()); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate(onCloudRun bool) error {
	// No stand-in for the database, not even in dev mode: `devbox run api` points at the
	// local one.
	if c.Database.URL == "" {
		return errors.New("database.url is required")
	}

	switch {
	case c.Doors.Bucket != "" && c.Doors.Dir != "":
		return errors.New("doors: set bucket or dir, not both")
	case c.Doors.Bucket == "" && c.Doors.Dir == "":
		return errors.New("doors.bucket or doors.dir is required")
	case c.Doors.Dir != "" && onCloudRun:
		return errors.New("doors.dir must not be set on Cloud Run, use doors.bucket")
	}

	if c.Dev {
		if onCloudRun {
			return errors.New("dev must not be set on Cloud Run")
		}
		if c.Resend.APIKey != "" && c.Resend.SegmentID == "" {
			return errors.New("resend.segmentId is required when resend.apiKey is set")
		}
		return nil
	}

	if c.Resend.APIKey == "" || c.Resend.SegmentID == "" || c.Subscribe.SigningKey == "" {
		return errors.New("resend.apiKey, resend.segmentId and subscribe.signingKey are required; set AOCN_DEV=true for local stand-ins")
	}
	return nil
}

// OnCloudRun reports whether the process runs as a Cloud Run service, which always
// sets K_SERVICE.
func OnCloudRun() bool {
	return os.Getenv("K_SERVICE") != ""
}
