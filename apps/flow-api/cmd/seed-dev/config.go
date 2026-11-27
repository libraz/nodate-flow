package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

//go:embed locales/*.json
var localesFS embed.FS

type seedConfig struct {
	dsn              string
	email            string
	password         string
	displayName      string
	user2Email       string
	user2Password    string
	user2DisplayName string
	workspaceSlug    string
	workspaceName    string
	locale           string
}

// seedLocale holds locale-specific default values for seed data.
// Loaded from locales/*.json via embed.FS.
type seedLocale struct {
	DisplayName            string     `json:"displayName"`
	WorkspaceName          string     `json:"workspaceName"`
	ProjectName            string     `json:"projectName"`
	ProjectDesc            string     `json:"projectDesc"`
	SecondUserDisplayName  string     `json:"secondUserDisplayName"`
	HolidaysCalendarName   string     `json:"holidaysCalendarName"`
	HolidayEventTitle      string     `json:"holidayEventTitle"`
	SampleEventTitle       string     `json:"sampleEventTitle"`
	SampleEventLocation    string     `json:"sampleEventLocation"`
	UndatedEventTitle      string     `json:"undatedEventTitle"`
	UndatedEventMemo       string     `json:"undatedEventMemo"`
	PublicShareTitle       string     `json:"publicShareTitle"`
	PublicShareDescription string     `json:"publicShareDescription"`
	Tasks                  []seedTask `json:"tasks"`
}

type seedTask struct {
	Title    string `json:"title"`
	Priority int32  `json:"priority"`
}

func loadLocale(name string) (seedLocale, error) {
	data, err := localesFS.ReadFile("locales/" + name + ".json")
	if err != nil {
		return seedLocale{}, fmt.Errorf("unsupported NF_SEED_LOCALE %q (add locales/%s.json to support it)", name, name)
	}
	var l seedLocale
	if err := json.Unmarshal(data, &l); err != nil {
		return seedLocale{}, fmt.Errorf("parse locales/%s.json: %w", name, err)
	}
	return l, nil
}

func loadConfig() (seedConfig, seedLocale, error) {
	locale := envOr("NF_SEED_LOCALE", "en")
	l, err := loadLocale(locale)
	if err != nil {
		return seedConfig{}, seedLocale{}, err
	}
	c := seedConfig{
		dsn:              os.Getenv("NF_DB_DSN"),
		email:            envOr("NF_SEED_EMAIL", "admin@example.com"),
		password:         envOr("NF_SEED_PASSWORD", "password123"),
		displayName:      envOr("NF_SEED_DISPLAY_NAME", l.DisplayName),
		user2Email:       envOr("NF_SEED_USER2_EMAIL", "alice@example.com"),
		user2Password:    envOr("NF_SEED_USER2_PASSWORD", "password123"),
		user2DisplayName: envOr("NF_SEED_USER2_DISPLAY_NAME", l.SecondUserDisplayName),
		workspaceSlug:    envOr("NF_SEED_WORKSPACE_SLUG", "demo"),
		workspaceName:    envOr("NF_SEED_WORKSPACE_NAME", l.WorkspaceName),
		locale:           locale,
	}
	if c.dsn == "" {
		return c, seedLocale{}, errors.New("NF_DB_DSN is required")
	}
	if len(c.password) < 8 {
		return c, seedLocale{}, errors.New("NF_SEED_PASSWORD must be >= 8 chars")
	}
	if len(c.user2Password) < 8 {
		return c, seedLocale{}, errors.New("NF_SEED_USER2_PASSWORD must be >= 8 chars")
	}
	return c, l, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
