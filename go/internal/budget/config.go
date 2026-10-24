package budget

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LoadConfig loads budget configuration and profiles from file and environment.
func LoadConfig(configPath, repoRoot string) (BudgetConfig, map[string]Profile, error) {
	cfg := DefaultConfig()
	profiles := StandardProfiles()

	targetPath := configPath
	if targetPath == "" && repoRoot != "" {
		candidates := []string{
			filepath.Join(repoRoot, ".adrian.toml"),
			filepath.Join(repoRoot, ".adrian/config.toml"),
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				targetPath = c
				break
			}
		}
	}

	if targetPath != "" {
		if err := parseTOMLFile(targetPath, &cfg, profiles); err != nil {
			return cfg, profiles, fmt.Errorf("failed to parse %s: %w", targetPath, err)
		}
	}

	applyEnvOverrides(&cfg)

	return cfg, profiles, nil
}

// ResolveProfile returns the requested profile by name, falling back to default or standard profile.
func ResolveProfile(profiles map[string]Profile, requestedName, defaultName string) Profile {
	name := strings.TrimSpace(requestedName)
	if name == "" {
		name = strings.TrimSpace(defaultName)
	}
	if name == "" {
		name = ProfileCloudStandard
	}

	if p, ok := profiles[name]; ok {
		return p
	}
	standard := StandardProfiles()
	if p, ok := standard[name]; ok {
		return p
	}
	return standard[ProfileCloudStandard]
}

func applyEnvOverrides(cfg *BudgetConfig) {
	if v := getFirstEnv("ADRIAN_BUDGET_PROFILE", "PR_REVIEW_BUDGET_PROFILE"); v != "" {
		cfg.DefaultProfile = v
	}
	if v := getFirstEnv("ADRIAN_BUDGET_MODE"); v != "" {
		cfg.Mode = v
	}
	if v := getFirstEnv("ADRIAN_FANOUT_THRESHOLD_BYTES", "PR_REVIEW_FANOUT_THRESHOLD_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.FanoutThresholdBytes = n
		}
	}
	if v := getFirstEnv("ADRIAN_FANOUT_THRESHOLD_SEGMENTS", "PR_REVIEW_FANOUT_THRESHOLD_SEGMENTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.FanoutThresholdSegments = n
		}
	}
	if v := getFirstEnv("ADRIAN_MAX_BATCH_BYTES", "PR_REVIEW_FANOUT_BATCH_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.BatchMaxBytes = n
		}
	}
	if v := getFirstEnv("ADRIAN_MAX_BATCH_SEGMENTS", "PR_REVIEW_FANOUT_BATCH_MAX_SEGMENTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BatchMaxSegments = n
		}
	}
}

func getFirstEnv(keys ...string) string {
	for _, k := range keys {
		if val := strings.TrimSpace(os.Getenv(k)); val != "" {
			return val
		}
	}
	return ""
}

func parseTOMLFile(path string, cfg *BudgetConfig, profiles map[string]Profile) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	currentSection := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Handle inline comments
		if idx := strings.Index(line, "#"); idx != -1 {
			// Ensure hash is not inside quotes
			inQuotes := false
			for i, r := range line {
				if r == '"' || r == '\'' {
					inQuotes = !inQuotes
				}
				if r == '#' && !inQuotes {
					line = strings.TrimSpace(line[:i])
					break
				}
			}
		}

		// Handle section headers
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := unquote(strings.TrimSpace(parts[1]))

		switch {
		case currentSection == "budget":
			switch key {
			case "mode":
				cfg.Mode = val
			case "default_profile":
				cfg.DefaultProfile = val
			case "fanout_threshold_bytes":
				if n, err := strconv.ParseInt(val, 10, 64); err == nil {
					cfg.FanoutThresholdBytes = n
				}
			case "fanout_threshold_segments":
				if n, err := strconv.Atoi(val); err == nil {
					cfg.FanoutThresholdSegments = n
				}
			case "batch_max_bytes":
				if n, err := strconv.ParseInt(val, 10, 64); err == nil {
					cfg.BatchMaxBytes = n
				}
			case "batch_max_segments":
				if n, err := strconv.Atoi(val); err == nil {
					cfg.BatchMaxSegments = n
				}
			case "safety_margin":
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					cfg.SafetyMargin = f
				}
			}

		case strings.HasPrefix(currentSection, "budget.profiles."):
			profileName := strings.TrimPrefix(currentSection, "budget.profiles.")
			p, ok := profiles[profileName]
			if !ok {
				p = Profile{Name: profileName, TokenRatio: 3.5, AllowFullFileContext: true, AllowADRBodies: true}
			}

			switch key {
			case "context_window":
				if n, err := strconv.Atoi(val); err == nil {
					p.ContextWindow = n
				}
			case "target_batch_budget":
				if n, err := strconv.Atoi(val); err == nil {
					p.TargetBatchBudget = n
				}
			case "token_ratio":
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					p.TokenRatio = f
				}
			case "max_parallel_agents":
				if n, err := strconv.Atoi(val); err == nil {
					p.MaxParallelAgents = n
				}
			case "preferred_adapter":
				p.PreferredAdapter = val
			case "allow_full_file_context":
				p.AllowFullFileContext = val == "true"
			case "allow_adr_bodies":
				p.AllowADRBodies = val == "true"
			}
			profiles[profileName] = p
		}
	}

	return scanner.Err()
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
