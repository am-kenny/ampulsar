package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
)

type section interface {
	fields() []fieldSpec
}

type defaulter interface {
	defaults()
}

// toggled config sections get a check if their config must be loaded
type toggled interface {
	toggle() (name string, dst *bool)
}

type fieldSpec struct {
	name     string
	parse    func(string) error
	required bool
}

// loadSection checks if group has toggled interface.
// If it has an interface and enabled is falsy, the group gets skipped.
// Otherwise loadFields is called on the group fields
func loadSection(s section) error {
	if t, ok := s.(toggled); ok {
		name, on := t.toggle()
		if v := os.Getenv(name); v != "" {
			if err := parseBool(on)(v); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		if !*on {
			return nil
		}
	}
	return loadFields(s.fields())
}

// loadFields populates each spec's target from its environment variable.
// A group missing required vars produces an error.
func loadFields(specs []fieldSpec) error {
	var missing []string

	for _, s := range specs {
		v := os.Getenv(s.name)
		if v == "" {
			if s.required {
				missing = append(missing, s.name)
			}
			continue
		}

		if err := s.parse(v); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}

	return nil
}

type TwitchConfig struct {
	Enabled      bool
	ClientID     string
	ClientSecret string
	ChannelName  string
}

// fields returns list of fieldSpec, holding env definitions
// and pointers into the TwitchConfig for env loading
func (cnf *TwitchConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"TWITCH_CLIENT_ID", parseString(&cnf.ClientID), true},
		{"TWITCH_CLIENT_SECRET", parseString(&cnf.ClientSecret), true},
		{"TWITCH_CHANNEL_NAME", parseString(&cnf.ChannelName), true},
	}
}

func (cnf *TwitchConfig) toggle() (string, *bool) {
	return "TWITCH_ENABLED", &cnf.Enabled
}

type TikTokConfig struct {
	Enabled  bool
	Username string
}

// fields returns list of fieldSpec, holding env definitions
// and pointers into the TikTokConfig for env loading
func (cnf *TikTokConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"TIKTOK_USERNAME", parseString(&cnf.Username), true},
	}
}

func (cnf *TikTokConfig) toggle() (string, *bool) {
	return "TIKTOK_ENABLED", &cnf.Enabled
}

type TelegramConfig struct {
	Enabled      bool
	BotToken     string
	ChatID       string
	EditOnChange bool
	OnEnd        domain.EndPolicy
	Pin          bool
}

// fields returns list of fieldSpec, holding env definitions
// and pointers into the TelegramConfig for env loading
func (cnf *TelegramConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"TELEGRAM_BOT_TOKEN", parseString(&cnf.BotToken), true},
		{"TELEGRAM_CHAT_ID", parseString(&cnf.ChatID), true},
		{"TELEGRAM_EDIT_ON_CHANGE", parseBool(&cnf.EditOnChange), false},
		{"TELEGRAM_ACTION_ON_END", parseEndPolicy(&cnf.OnEnd), false},
		{"TELEGRAM_PIN", parseBool(&cnf.Pin), false},
	}
}

func (cnf *TelegramConfig) toggle() (string, *bool) {
	return "TELEGRAM_ENABLED", &cnf.Enabled
}

func (cnf *TelegramConfig) defaults() {
	cnf.OnEnd = domain.EndPolicyEditInPlace
	// EditOnChange and Pin default to false on init
}

type DiscordConfig struct {
	Enabled   bool
	BotToken  string
	ChannelID string
}

// fields returns list of fieldSpec, holding env definitions
// and pointers into the DiscordConfig for env loading
func (cnf *DiscordConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"DISCORD_BOT_TOKEN", parseString(&cnf.BotToken), true},
		{"DISCORD_CHANNEL_ID", parseString(&cnf.ChannelID), true},
	}
}

func (cnf *DiscordConfig) toggle() (string, *bool) {
	return "DISCORD_ENABLED", &cnf.Enabled
}

type TemplateConfig struct {
	Style    string
	Language string
}

// fields returns list of fieldSpec, holding env definitions
// and pointers into the TemplateConfig for env loading
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
	EndGrace time.Duration
}

// fields returns list of fieldSpec, holding env definitions
// and pointers into the PollConfig for env loading
func (cnf *PollConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"POLL_INTERVAL", parseDuration(&cnf.Interval), false},
		{"POLL_END_GRACE", parseDuration(&cnf.EndGrace), false},
	}
}

func (cnf *PollConfig) defaults() {
	cnf.Interval = 1 * time.Minute
	cnf.EndGrace = 10 * time.Minute
}

type ReconcileConfig struct {
	Interval time.Duration
}

// fields returns list of fieldSpec, holding env definitions
// and pointers into the ReconcileConfig for env loading
func (cnf *ReconcileConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"RECONCILE_INTERVAL", parseDuration(&cnf.Interval), false},
	}
}

func (cnf *ReconcileConfig) defaults() {
	cnf.Interval = 1 * time.Minute
}

type StoreConfig struct {
	Path string
}

// fields returns list of fieldSpec, holding env definitions
// and pointers into the StoreConfig for env loading
func (cnf *StoreConfig) fields() []fieldSpec {
	return []fieldSpec{
		{"STORE_PATH", parseString(&cnf.Path), false},
	}
}

type Config struct {
	Twitch    TwitchConfig
	TikTok    TikTokConfig
	Telegram  TelegramConfig
	Discord   DiscordConfig
	Template  TemplateConfig
	Poll      PollConfig
	Reconcile ReconcileConfig
	Store     StoreConfig
}

func (cfg *Config) sections() []section {
	return []section{
		&cfg.Twitch,
		&cfg.TikTok,
		&cfg.Telegram,
		&cfg.Discord,
		&cfg.Template,
		&cfg.Poll,
		&cfg.Reconcile,
		&cfg.Store,
	}
}

func (cfg *Config) validate() error {
	if !cfg.Twitch.Enabled && !cfg.TikTok.Enabled {
		return fmt.Errorf("no source platform enabled")
	}

	// Only one source can run at a time for now
	if cfg.Twitch.Enabled && cfg.TikTok.Enabled {
		return fmt.Errorf("only one source platform at a time is supported")
	}

	// if !cfg.Discord.Enabled && !cfg.Telegram.Enabled {
	// 	return fmt.Errorf("no receiving platform enabled")
	// }

	// Discord is unsupported for now
	if !cfg.Telegram.Enabled {
		return fmt.Errorf("no receiving platform enabled")
	}

	return nil
}

// Load reads configuration from environment variables, validates it
// and returns a populated Config or an error.
func Load() (*Config, error) {
	cfg := &Config{}

	for _, s := range cfg.sections() {
		if d, ok := s.(defaulter); ok {
			d.defaults()
		}

		if err := loadSection(s); err != nil {
			return nil, err
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

func parseEndPolicy(dst *domain.EndPolicy) func(string) error {
	return func(v string) error {
		p := domain.EndPolicy(v)
		if !p.Valid() {
			return fmt.Errorf("want edit_in_place, new_message, replace, delete or none, got %q", v)
		}
		*dst = p
		return nil
	}
}
