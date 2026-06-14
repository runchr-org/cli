package review

import (
	"context"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
	"github.com/entireio/cli/cmd/entire/cli/settings"
)

const (
	tAgentClaude = "claude-code"
	tAgentCodex  = "codex"
	tModelOpus   = "opus"
	tModelSonnet = "sonnet"
)

func TestModelInList(t *testing.T) {
	models := []agent.ModelInfo{{ID: "opus"}, {ID: "sonnet"}}
	if !modelInList("opus", models) {
		t.Error("expected opus to be in list")
	}
	if modelInList("gpt-5", models) {
		t.Error("gpt-5 should not be in list")
	}
}

func configureTestDeps(adapter ...string) Deps {
	set := map[string]struct{}{}
	for _, a := range adapter {
		set[a] = struct{}{}
	}
	return Deps{
		ReviewerFor: func(name string) reviewtypes.AgentReviewer {
			if _, ok := set[name]; ok {
				return &stubReviewer{name: name}
			}
			return nil
		},
	}
}

func TestBuildConfiguredProfile_FromFlags(t *testing.T) {
	deps := configureTestDeps("claude-code", "codex")
	profile, err := buildConfiguredProfile(
		context.Background(),
		"general",
		reviewConfigureOptions{
			Agents: []string{"claude-code", "codex"},
			Judges: []string{"codex"},
			Models: []string{"claude-code=opus"},
		},
		&settings.EntireSettings{},
		deps,
	)
	if err != nil {
		t.Fatalf("buildConfiguredProfile: %v", err)
	}
	if len(profile.Agents) != 2 {
		t.Fatalf("agents = %d, want 2: %#v", len(profile.Agents), profile.Agents)
	}
	if got := profile.Agents["claude-code"].Model; got != "opus" {
		t.Errorf("claude-code model = %q, want opus", got)
	}
	if len(profile.Judges) != 1 || profile.Judges[0].Agent != "codex" {
		t.Errorf("judges = %#v, want one judge codex", profile.Judges)
	}
	if profile.Master != "" {
		t.Errorf("legacy master should be cleared when judges are set, got %q", profile.Master)
	}
	if profile.Task == "" {
		t.Error("task should default to the built-in general task")
	}
}

func TestBuildConfiguredProfile_FromSlots_AllowsDuplicateAgents(t *testing.T) {
	deps := configureTestDeps("claude-code", "codex")
	profile, err := buildConfiguredProfile(
		context.Background(),
		"general",
		reviewConfigureOptions{
			Slots: []string{tAgentClaude + "=" + tModelOpus, tAgentClaude + "=" + tModelSonnet, tAgentClaude, tAgentClaude},
		},
		&settings.EntireSettings{},
		deps,
	)
	if err != nil {
		t.Fatalf("buildConfiguredProfile: %v", err)
	}
	// Four distinct workers: two different models + two identical default slots.
	if len(profile.Agents) != 4 {
		t.Fatalf("agents = %d, want 4: %#v", len(profile.Agents), profile.Agents)
	}
	models := map[string]int{}
	for _, cfg := range profile.Agents {
		if cfg.Agent != tAgentClaude {
			t.Errorf("worker agent = %q, want claude-code", cfg.Agent)
		}
		models[cfg.Model]++
	}
	if models[tModelOpus] != 1 || models[tModelSonnet] != 1 || models[""] != 2 {
		t.Errorf("model distribution = %#v, want opus:1 sonnet:1 default:2", models)
	}
}

func TestProfileMasterIdentity(t *testing.T) {
	t.Run("standalone master wins and need not be a worker", func(t *testing.T) {
		profile := settings.ReviewProfileConfig{
			Agents:      map[string]settings.ReviewConfig{tAgentCodex: {Agent: tAgentCodex}},
			MasterAgent: tAgentClaude,
			MasterModel: tModelOpus,
		}
		name, model, ok := profileMasterIdentity(profile)
		if !ok || name != tAgentClaude || model != tModelOpus {
			t.Fatalf("got (%q,%q,%v), want (claude-code, opus, true)", name, model, ok)
		}
	})
	t.Run("legacy worker master resolves from Agents", func(t *testing.T) {
		profile := settings.ReviewProfileConfig{
			Agents: map[string]settings.ReviewConfig{tAgentClaude: {Agent: tAgentClaude, Model: tModelSonnet}},
			Master: tAgentClaude,
		}
		name, model, ok := profileMasterIdentity(profile)
		if !ok || name != tAgentClaude || model != tModelSonnet {
			t.Fatalf("got (%q,%q,%v), want (claude-code, sonnet, true)", name, model, ok)
		}
	})
	t.Run("no master", func(t *testing.T) {
		profile := settings.ReviewProfileConfig{
			Agents: map[string]settings.ReviewConfig{tAgentCodex: {Agent: tAgentCodex}},
		}
		if _, _, ok := profileMasterIdentity(profile); ok {
			t.Fatal("expected ok=false when no master is set")
		}
	})
}

func TestBuildConfiguredProfile_JudgePanel(t *testing.T) {
	deps := configureTestDeps("claude-code", "codex")
	profile, err := buildConfiguredProfile(
		context.Background(),
		"general",
		reviewConfigureOptions{
			Agents: []string{tAgentClaude, tAgentCodex},
			Judges: []string{tAgentClaude + "=" + tModelOpus, tAgentCodex + "=gpt-5"},
			Chair:  tAgentClaude,
		},
		&settings.EntireSettings{},
		deps,
	)
	if err != nil {
		t.Fatalf("buildConfiguredProfile: %v", err)
	}
	if len(profile.Judges) != 2 {
		t.Fatalf("judges = %#v, want 2", profile.Judges)
	}
	if profile.Judges[0].Agent != tAgentClaude || profile.Judges[0].Model != tModelOpus {
		t.Errorf("judge[0] = %#v, want claude-code/opus", profile.Judges[0])
	}
	if profile.Chair != tAgentClaude {
		t.Errorf("chair = %q, want claude-code", profile.Chair)
	}
	// profileJudges resolves the panel + chair index.
	judges, chair := profileJudges(profile)
	if len(judges) != 2 || chair != 0 {
		t.Errorf("profileJudges = (%#v, %d), want 2 judges, chair 0", judges, chair)
	}
}

func TestBuildConfiguredProfile_RejectsNonAdapterAgent(t *testing.T) {
	deps := configureTestDeps("claude-code")
	_, err := buildConfiguredProfile(
		context.Background(),
		"general",
		reviewConfigureOptions{Agents: []string{"cursor"}},
		&settings.EntireSettings{},
		deps,
	)
	if err == nil {
		t.Fatal("expected error for agent without a review-runner adapter")
	}
}

func TestBuildConfiguredProfile_PreservesExistingTaskAndMasterModel(t *testing.T) {
	deps := configureTestDeps("claude-code", "codex")
	s := &settings.EntireSettings{
		ReviewProfiles: map[string]settings.ReviewProfileConfig{
			"general": {
				Task:        "Custom task text.",
				MasterModel: "opus",
				Agents: map[string]settings.ReviewConfig{
					"claude-code": {Skills: []string{"/review"}},
				},
				Master: "claude-code",
			},
		},
	}
	// Only change the worker set; task + master_model must survive.
	profile, err := buildConfiguredProfile(
		context.Background(),
		"general",
		reviewConfigureOptions{Agents: []string{"claude-code", "codex"}},
		s,
		deps,
	)
	if err != nil {
		t.Fatalf("buildConfiguredProfile: %v", err)
	}
	if profile.Task != "Custom task text." {
		t.Errorf("task = %q, want preserved custom task", profile.Task)
	}
	if profile.MasterModel != "opus" {
		t.Errorf("master_model = %q, want preserved opus", profile.MasterModel)
	}
}

func TestBuildConfiguredProfile_InvalidModelSpec(t *testing.T) {
	deps := configureTestDeps("claude-code")
	_, err := buildConfiguredProfile(
		context.Background(),
		"general",
		reviewConfigureOptions{Agents: []string{"claude-code"}, Models: []string{"no-equals"}},
		&settings.EntireSettings{},
		deps,
	)
	if err == nil {
		t.Fatal("expected error for malformed --set-model spec")
	}
}
