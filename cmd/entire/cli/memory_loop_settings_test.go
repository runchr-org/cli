package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/stretchr/testify/require"
)

func TestLoadEntireSettings_MemoryLoopConfig(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "enabled": true,
  "memory_loop": {
    "enabled": true,
    "claude_injection_enabled": false,
    "max_injected": 4,
    "default_refresh_window": 12
  }
}`), 0o644))
	t.Chdir(tmpDir)

	loaded, err := LoadEntireSettings(context.Background())
	require.NoError(t, err)
	cfg := loaded.GetMemoryLoopConfig()
	require.True(t, cfg.Enabled)
	require.Equal(t, "manual", cfg.Mode)
	require.Equal(t, "review", cfg.ActivationPolicy)
	require.Equal(t, 4, cfg.MaxInjected)
	require.Equal(t, 12, cfg.DefaultRefreshWindow)
}

func TestLoadEntireSettings_MemoryLoopLegacyEnabledAndInjectionMapping(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "enabled": true,
  "memory_loop": {
    "enabled": true,
    "claude_injection_enabled": true
  }
}`), 0o644))
	t.Chdir(tmpDir)

	loaded, err := LoadEntireSettings(context.Background())
	require.NoError(t, err)
	cfg := loaded.GetMemoryLoopConfig()
	require.True(t, cfg.Enabled)
	require.Equal(t, "auto", cfg.Mode)
}

func TestLoadEntireSettings_MemoryLoopModeAndPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "enabled": true,
  "memory_loop": {
    "enabled": true,
    "mode": "manual",
    "activation_policy": "auto",
    "max_injected": 7
  }
}`), 0o644))
	t.Chdir(tmpDir)

	loaded, err := LoadEntireSettings(context.Background())
	require.NoError(t, err)
	cfg := loaded.GetMemoryLoopConfig()
	require.True(t, cfg.Enabled)
	require.Equal(t, "manual", cfg.Mode)
	require.Equal(t, "auto", cfg.ActivationPolicy)
	require.Equal(t, 7, cfg.MaxInjected)
}

func TestLoadEntireSettings_MemoryLoopExplicitModeOverridesLegacyEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "enabled": true,
  "memory_loop": {
    "enabled": false,
    "mode": "auto"
  }
}`), 0o644))
	t.Chdir(tmpDir)

	loaded, err := LoadEntireSettings(context.Background())
	require.NoError(t, err)
	cfg := loaded.GetMemoryLoopConfig()
	require.Equal(t, "auto", cfg.Mode)
}

func TestLoadEntireSettings_MemoryLoopLocalOverrideMergesFields(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "enabled": true,
  "memory_loop": {
    "mode": "manual",
    "activation_policy": "auto",
    "max_injected": 3
  }
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsLocalFile), []byte(`{
  "memory_loop": {
    "max_injected": 9
  }
}`), 0o644))
	t.Chdir(tmpDir)

	loaded, err := LoadEntireSettings(context.Background())
	require.NoError(t, err)
	cfg := loaded.GetMemoryLoopConfig()
	require.Equal(t, "manual", cfg.Mode)
	require.Equal(t, "auto", cfg.ActivationPolicy)
	require.Equal(t, 9, cfg.MaxInjected)
}

func TestLoadEntireSettings_InvalidMemoryLoopMode(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "enabled": true,
  "memory_loop": {
    "mode": "sideways"
  }
}`), 0o644))
	t.Chdir(tmpDir)

	_, err := LoadEntireSettings(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid memory_loop.mode")
}

func TestLoadEntireSettings_InvalidMemoryLoopActivationPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "enabled": true,
  "memory_loop": {
    "activation_policy": "sometimes"
  }
}`), 0o644))
	t.Chdir(tmpDir)

	_, err := LoadEntireSettings(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid memory_loop.activation_policy")
}

func TestLoadEntireSettings_MemoryLoopDefaultsMaxInjectedToTwo(t *testing.T) {
	t.Parallel()

	loaded := &settings.EntireSettings{}

	cfg := loaded.GetMemoryLoopConfig()
	require.Equal(t, 2, cfg.MaxInjected)
}
