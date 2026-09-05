package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type fieldSpec struct {
	name     string
	parse    func(string) error
	required bool
}

// populates each spec's target from its environment variable
func loadFields(specs []fieldSpec) error {
	for _, s := range specs {
		v := os.Getenv(s.name)
		if v == "" {
			if s.required {
				return fmt.Errorf("%s is required", s.name)
			}
			continue
		}

		if err := s.parse(v); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

type EndPolicy string

const (
	EndPolicyEditInPlace EndPolicy = "edit_in_place"
	EndPolicyNewMessage  EndPolicy = "new_message"
	EndPolicyDelete      EndPolicy = "delete"
	EndPolicyNone        EndPolicy = "none"
)

func (p EndPolicy) Valid() bool {
	switch p {
	case EndPolicyEditInPlace, EndPolicyNewMessage, EndPolicyDelete, EndPolicyNone:
		return true
	}
	return false
}

type TwitchConfig struct {
	ClientID     string
	ClientSecret string
	ChannelName  string
}

// returns list of fieldSpec, holding env definitions and
// pointers into the TwitchConfig for env loading
func (cnf *TwitchConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"TWITCH_CLIENT_ID", parseString(&cnf.ClientID), true},
		{"TWITCH_CLIENT_SECRET", parseString(&cnf.ClientSecret), true},
		{"TWITCH_CHANNEL_NAME", parseString(&cnf.ChannelName), true},
	}
}

func (cnf *TwitchConfig) Active() bool {
	return cnf.ClientID != "" && cnf.ClientSecret != "" && cnf.ChannelName != ""
}

type TelegramConfig struct {
	BotToken     string
	ChatID       string
	EditOnChange bool
	OnEnd        EndPolicy
	Pin          bool
}

// returns list of fieldSpec, holding env definitions and
// pointers into the TelegramConfig for env loading
func (cnf *TelegramConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"TELEGRAM_BOT_TOKEN", parseString(&cnf.BotToken), true},
		{"TELEGRAM_CHAT_ID", parseString(&cnf.ChatID), true},
		{"TELEGRAM_EDIT_ON_CHANGE", parseBool(&cnf.EditOnChange), false},
		{"TELEGRAM_ACTION_ON_END", parseEndPolicy(&cnf.OnEnd), false},
		{"TELEGRAM_PIN", parseBool(&cnf.Pin), false},
	}
}

func (cnf *TelegramConfig) Active() bool {
	return cnf.BotToken != "" && cnf.ChatID != ""
}

func (cnf *TelegramConfig) defaults() {
	cnf.OnEnd = EndPolicyEditInPlace
	// EditOnChange and Pin default to false on init
}

type DiscordConfig struct {
	BotToken  string
	ChannelID string
}

// returns list of fieldSpec, holding env definitions and
// pointers into the DiscordConfig for env loading
func (cnf *DiscordConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"DISCORD_BOT_TOKEN", parseString(&cnf.BotToken), false},
		{"DISCORD_CHANNEL_ID", parseString(&cnf.ChannelID), false},
	}
}

func (cnf *DiscordConfig) Active() bool {
	return cnf.BotToken != "" && cnf.ChannelID != ""
}

type TemplateConfig struct {
	Style    string
	Language string
}

func (cnf *TemplateConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"TEMPLATE_STYLE", parseString(&cnf.Style), false},
		{"TEMPLATE_LANGUAGE", parseString(&cnf.Language), false},
	}
}

func (cnf *TemplateConfig) defaults() {
	cnf.Style = "default"
	cnf.Language = "ru"
}

type PollConfig struct {
	Interval time.Duration
}

func (cnf *PollConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"POLL_INTERVAL", parseDuration(&cnf.Interval), false},
	}
}

func (cnf *PollConfig) defaults() {
	cnf.Interval = 5 * time.Minute
}

type StoreConfig struct {
	Path string
}

func (cnf *StoreConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"STORE_PATH", parseString(&cnf.Path), false},
	}
}

type Config struct {
	Twitch   TwitchConfig
	Telegram TelegramConfig
	Discord  DiscordConfig
	Template TemplateConfig
	Poll     PollConfig
	Store    StoreConfig
}

func (cfg *Config) validate() error {
	if !cfg.Discord.Active() && !cfg.Telegram.Active() {
		return fmt.Errorf("no receiving platform configured")
	}

	return nil
}

// Load reads configuration from environment variables, validates it
// and returns a populated Config or an error.
func Load() (*Config, error) {
	cfg := &Config{}

	cfg.Telegram.defaults()
	cfg.Template.defaults()
	cfg.Poll.defaults()

	groups := [][]fieldSpec{
		cfg.Twitch.fields(),
		cfg.Telegram.fields(),
		cfg.Discord.fields(),
		cfg.Template.fields(),
		cfg.Poll.fields(),
		cfg.Store.fields(),
	}

	for _, specs := range groups {
		if err := loadFields(specs); err != nil {
			return nil, fmt.Errorf("failed to load fields: %w", err)
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func parseString(dst *string) func(string) error {
	return func(v string) error { *dst = v; return nil }
}

func parseBool(dst *bool) func(string) error {
	return func(v string) error {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("want a boolean, got %q", v)
		}
		*dst = b
		return nil
	}
}

func parseDuration(dst *time.Duration) func(string) error {
	return func(v string) error {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("want a duration like 90s or 5m, got %q", v)
		}
		if d <= 0 {
			return fmt.Errorf("must be positive, got %q", v)
		}
		*dst = d
		return nil
	}
}

func parseEndPolicy(dst *EndPolicy) func(string) error {
	return func(v string) error {
		p := EndPolicy(v)
		if !p.Valid() {
			return fmt.Errorf("want edit_in_place, new_message, delete or none, got %q", v)
		}
		*dst = p
		return nil
	}
}
