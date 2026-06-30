package config

import (
	"fmt"
	"os"
	"strings"
)

type fieldSpec struct {
	name     string
	value    *string
	required bool
}

// populates each spec's target from its environment variable
func loadFields(specs []fieldSpec) {
	for _, s := range specs {
		*s.value = os.Getenv(s.name)
	}
}

func validateFields(groupName string, specs []fieldSpec) error {
	var missing []string
	hasAny := false

	for _, s := range specs {
		if *s.value == "" {
			if s.required {
				return fmt.Errorf("missing required env var: %s", s.name)
			}
			missing = append(missing, s.name)
		} else {
			hasAny = true
		}
	}

	if hasAny && len(missing) > 0 {
		return fmt.Errorf("incomplete %s configuration: missing %s", groupName, strings.Join(missing, ", "))
	}

	return nil
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
		{"TWITCH_CLIENT_ID", &cnf.ClientID, true},
		{"TWITCH_CLIENT_SECRET", &cnf.ClientSecret, true},
		{"TWITCH_CHANNEL_NAME", &cnf.ChannelName, true},
	}
}

func (cnf *TwitchConfig) validate() error {
	return validateFields("Twitch", cnf.fields())
}

func (cnf *TwitchConfig) Active() bool {
	return cnf.ClientID != "" && cnf.ClientSecret != "" && cnf.ChannelName != ""
}

type TelegramConfig struct {
	BotToken string
	ChatID   string
}

// returns list of fieldSpec, holding env definitions and
// pointers into the TelegramConfig for env loading
func (cnf *TelegramConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"TELEGRAM_BOT_TOKEN", &cnf.BotToken, false},
		{"TELEGRAM_CHAT_ID", &cnf.ChatID, false},
	}
}

func (cnf *TelegramConfig) validate() error {
	return validateFields("Telegram", cnf.fields())
}

func (cnf *TelegramConfig) Active() bool {
	return cnf.BotToken != "" && cnf.ChatID != ""
}

type DiscordConfig struct {
	BotToken  string
	ChannelID string
}

// returns list of fieldSpec, holding env definitions and
// pointers into the DiscordConfig for env loading
func (cnf *DiscordConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"DISCORD_BOT_TOKEN", &cnf.BotToken, false},
		{"DISCORD_CHANNEL_ID", &cnf.ChannelID, false},
	}
}

func (cnf *DiscordConfig) validate() error {
	return validateFields("Discord", cnf.fields())
}

func (cnf *DiscordConfig) Active() bool {
	return cnf.BotToken != "" && cnf.ChannelID != ""
}

type Config struct {
	Twitch   TwitchConfig
	Telegram TelegramConfig
	Discord  DiscordConfig
}

func (cfg *Config) validate() error {
	if err := cfg.Twitch.validate(); err != nil {
		return err
	}

	if err := cfg.Telegram.validate(); err != nil {
		return err
	}

	if err := cfg.Discord.validate(); err != nil {
		return err
	}

	if !cfg.Discord.Active() && !cfg.Telegram.Active() {
		return fmt.Errorf("no receiving platform configured")
	}

	return nil
}

// Load reads configuration from environment variables, validates it
// and returns a populated Config or an error.
func Load() (*Config, error) {
	cfg := &Config{}

	groups := [][]fieldSpec{
		cfg.Twitch.fields(),
		cfg.Telegram.fields(),
		cfg.Discord.fields(),
	}

	for _, specs := range groups {
		loadFields(specs)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}
