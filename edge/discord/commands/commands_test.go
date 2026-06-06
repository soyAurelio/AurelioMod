// Package commands_test tests slash command definitions and response
// formatting for the edge-discord bot's /moderate, /status, and /config commands.
package commands_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/soyAurelio/AurelioMod/edge/discord/commands"
)

func TestCommandDefs_hasThreeCommands(t *testing.T) {
	defs := commands.CommandDefs()
	if len(defs) != 3 {
		t.Fatalf("expected 3 command definitions, got %d", len(defs))
	}

	names := make([]string, len(defs))
	for i, cmd := range defs {
		names[i] = cmd.CommandName()
	}

	if names[0] != "moderate" {
		t.Errorf("command 0: want moderate, got %s", names[0])
	}
	if names[1] != "status" {
		t.Errorf("command 1: want status, got %s", names[1])
	}
	if names[2] != "config" {
		t.Errorf("command 2: want config, got %s", names[2])
	}
}

func TestCommandDefs_moderateHasURLOption(t *testing.T) {
	defs := commands.CommandDefs()
	moderate := defs[0]

	slash, ok := moderate.(discord.SlashCommandCreate)
	if !ok {
		t.Fatal("moderate command should be a SlashCommandCreate")
	}

	data, err := json.Marshal(slash)
	if err != nil {
		t.Fatalf("failed to marshal moderate command: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if parsed["name"] != "moderate" {
		t.Errorf("name: want moderate, got %v", parsed["name"])
	}

	opts, ok := parsed["options"].([]interface{})
	if !ok || len(opts) == 0 {
		t.Fatal("moderate command should have options")
	}

	opt := opts[0].(map[string]interface{})
	if opt["name"] != "url" {
		t.Errorf("option name: want url, got %v", opt["name"])
	}
	if req, ok := opt["required"]; !ok || req != true {
		t.Error("url option should be required")
	}
}

func TestCommandDefs_statusIsValid(t *testing.T) {
	defs := commands.CommandDefs()
	status := defs[1]

	if status.CommandName() != "status" {
		t.Errorf("expected status, got %s", status.CommandName())
	}

	slash, ok := status.(discord.SlashCommandCreate)
	if !ok {
		t.Fatal("status command should be SlashCommandCreate")
	}
	if slash.Description == "" {
		t.Error("status command should have a description")
	}
}

func TestCommandDefs_configHasSubCommands(t *testing.T) {
	defs := commands.CommandDefs()
	config := defs[2]

	if config.CommandName() != "config" {
		t.Errorf("expected config, got %s", config.CommandName())
	}

	slash, ok := config.(discord.SlashCommandCreate)
	if !ok {
		t.Fatal("config command should be SlashCommandCreate")
	}

	data, err := json.Marshal(slash)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	opts, ok := parsed["options"].([]interface{})
	if !ok || len(opts) < 2 {
		t.Fatalf("config should have subcommand options, got %v", parsed["options"])
	}

	for i, opt := range opts {
		o := opt.(map[string]interface{})
		if tp, ok := o["type"].(float64); !ok || tp != 1.0 {
			t.Errorf("config option %d: type should be 1 (subcommand), got %v", i, tp)
		}
	}
}

func TestFormatModerateReply_BLOCK(t *testing.T) {
	result := commands.FormatModerateReply(commands.ModerateResponse{
		URL:         "https://example.com/bad.jpg",
		Decision:    "BLOCK",
		BlockReason: "violence_graphic",
		Confidence:  0.94,
	})

	if !strings.Contains(result, "BLOCKED") {
		t.Error("BLOCK reply should contain 'BLOCKED'")
	}
	if !strings.Contains(result, "violence_graphic") {
		t.Error("BLOCK reply should contain block reason")
	}
	if strings.Contains(result, "https://example.com/bad.jpg") {
		t.Error("BLOCK reply should NOT contain URL (privacy)")
	}
	if !strings.Contains(result, "94") {
		t.Error("BLOCK reply should contain confidence")
	}
}

func TestFormatModerateReply_ALLOW(t *testing.T) {
	result := commands.FormatModerateReply(commands.ModerateResponse{
		URL:      "https://example.com/ok.jpg",
		Decision: "ALLOW",
	})

	if !strings.Contains(result, "ALLOWED") {
		t.Error("ALLOW reply should contain 'ALLOWED'")
	}
	if strings.Contains(result, "https://example.com/ok.jpg") {
		t.Error("ALLOW reply should NOT contain URL (privacy)")
	}
	if strings.Contains(result, "Reason") {
		t.Error("ALLOW reply should NOT contain 'Reason'")
	}
}

func TestFormatModerateReply_QUEUED(t *testing.T) {
	result := commands.FormatModerateReply(commands.ModerateResponse{
		URL:      "https://example.com/new.jpg",
		Decision: "QUEUED",
	})

	if !strings.Contains(result, "QUEUED") {
		t.Error("QUEUED reply should contain 'QUEUED'")
	}
	if !strings.Contains(result, "analysis") {
		t.Error("QUEUED reply should mention analysis")
	}
}

func TestFormatModerateReply_unspecifiedDecision(t *testing.T) {
	result := commands.FormatModerateReply(commands.ModerateResponse{
		URL:      "https://example.com/test.jpg",
		Decision: "UNSPECIFIED",
	})

	if !strings.Contains(result, "Unknown Decision") {
		t.Errorf("unspecified decision should show 'Unknown Decision', got: %s", result)
	}
}

func TestFormatModerateReply_ERROR(t *testing.T) {
	result := commands.FormatModerateReply(commands.ModerateResponse{
		URL:      "https://x.com",
		Decision: "ERROR",
	})

	if !strings.Contains(result, "ERROR") {
		t.Errorf("ERROR decision should show error, got: %s", result)
	}
}

func TestFormatStatusReply_healthy(t *testing.T) {
	info := commands.StatusInfo{
		Uptime:        2*time.Hour + 15*time.Minute,
		EngineHealthy: true,
		BreakerState:  "closed",
		TokensAvail:   45.0,
	}

	result := commands.FormatStatusReply(info)

	checks := []string{"healthy", "2h", "closed"}
	for _, c := range checks {
		if !strings.Contains(result, c) {
			t.Errorf("status reply missing '%s': %s", c, result)
		}
	}
}

func TestFormatStatusReply_unhealthy(t *testing.T) {
	info := commands.StatusInfo{
		Uptime:        30 * time.Minute,
		EngineHealthy: false,
		BreakerState:  "open",
		TokensAvail:   0.0,
	}

	result := commands.FormatStatusReply(info)

	checks := []string{"unhealthy", "open", "30m"}
	for _, c := range checks {
		if !strings.Contains(result, c) {
			t.Errorf("status reply missing '%s': %s", c, result)
		}
	}
}
