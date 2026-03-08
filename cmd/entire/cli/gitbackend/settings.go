package gitbackend

// ConfigFromSettings converts settings-level git backend configuration
// (using plain strings) into a typed gitbackend.Config.
// Returns nil if the input is nil (use all defaults).
func ConfigFromSettings(defaultProvider string, overrides map[string]string) *Config {
	if defaultProvider == "" && len(overrides) == 0 {
		return nil
	}

	cfg := &Config{
		Default: Provider(defaultProvider),
	}

	if len(overrides) > 0 {
		cfg.Overrides = make(map[OpCategory]Provider, len(overrides))
		for cat, prov := range overrides {
			cfg.Overrides[OpCategory(cat)] = Provider(prov)
		}
	}

	return cfg
}
