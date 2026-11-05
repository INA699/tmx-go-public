package config

// On reports whether Companion was explicitly enabled. Trial loading always disables it.
func (c *CompanionConfig) On() bool { return c != nil && c.Enabled != nil && *c.Enabled }
