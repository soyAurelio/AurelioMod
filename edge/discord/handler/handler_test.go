// Package handler provides decision enforcement for Discord moderation.
// Tests cover BLOCK/ALLOW/QUEUED enforcement, ENFORCE_MODERATION gate,
// and DM failure resilience per spec §discord-moderation.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"

	"github.com/soyAurelio/AurelioMod/edge/discord/audit"
)

// mockDiscordRest records REST calls made by the handler for test assertions.
type mockDiscordRest struct {
	deleteCalls []deleteCall
	dmCalls     []dmCall
	msgCalls    []msgCall

	deleteErr error
	dmErr     error
	dmChannel *discord.DMChannel
}

type deleteCall struct {
	channelID snowflake.ID
	messageID snowflake.ID
}

type dmCall struct {
	userID snowflake.ID
}

type msgCall struct {
	channelID snowflake.ID
	content   string
}

func (m *mockDiscordRest) DeleteMessage(channelID snowflake.ID, messageID snowflake.ID, opts ...rest.RequestOpt) error {
	m.deleteCalls = append(m.deleteCalls, deleteCall{channelID: channelID, messageID: messageID})
	return m.deleteErr
}

func (m *mockDiscordRest) CreateDMChannel(userID snowflake.ID, opts ...rest.RequestOpt) (*discord.DMChannel, error) {
	m.dmCalls = append(m.dmCalls, dmCall{userID: userID})
	if m.dmChannel != nil {
		return m.dmChannel, m.dmErr
	}
	return &discord.DMChannel{}, m.dmErr
}

func (m *mockDiscordRest) CreateMessage(channelID snowflake.ID, messageCreate discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error) {
	m.msgCalls = append(m.msgCalls, msgCall{channelID: channelID, content: messageCreate.Content})
	return &discord.Message{}, nil
}

// testEmitter captures audit events for verification.
type testEmitter struct {
	events []audit.AuditEvent
}

func (e *testEmitter) Emit(ctx context.Context, event audit.AuditEvent) error {
	e.events = append(e.events, event)
	return nil
}

// Compile-time interface checks.
var _ audit.Emitter = (*testEmitter)(nil)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testMessage(channelID, messageID, authorID snowflake.ID) *discord.Message {
	return &discord.Message{
		ID:        messageID,
		ChannelID: channelID,
		GuildID:   new(snowflake.ID(11111)),
		Author: discord.User{
			ID:       authorID,
			Username: "testuser",
		},
		Content: "test content",
	}
}

// testSnowflakes returns fixed snowflake IDs for test consistency.
func testSnowflakes() (channelID, messageID, authorID snowflake.ID) {
	return snowflake.ID(100), snowflake.ID(200), snowflake.ID(300)
}

func TestEnforceDecision_BLOCK_deletesAndNotifies(t *testing.T) {
	// Scenario: BLOCK decision → delete message + DM author with block_reason + audit event.
	ctx := t.Context()
	channelID, messageID, authorID := testSnowflakes()
	msg := testMessage(channelID, messageID, authorID)

	mock := &mockDiscordRest{dmChannel: &discord.DMChannel{}}
	emitter := &testEmitter{}

	h := NewWithEnforce(mock, emitter, testLogger(), true)

	err := h.EnforceDecision(ctx, msg, aureliomodv1.Decision_DECISION_BLOCK, "violence_graphic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify delete was called
	if len(mock.deleteCalls) != 1 {
		t.Fatalf("expected 1 delete call, got %d", len(mock.deleteCalls))
	}
	if mock.deleteCalls[0].channelID != channelID {
		t.Errorf("delete channelID: want %d, got %d", channelID, mock.deleteCalls[0].channelID)
	}
	if mock.deleteCalls[0].messageID != messageID {
		t.Errorf("delete messageID: want %d, got %d", messageID, mock.deleteCalls[0].messageID)
	}

	// Verify DM channel was created
	if len(mock.dmCalls) != 1 {
		t.Fatalf("expected 1 DM call, got %d", len(mock.dmCalls))
	}
	if mock.dmCalls[0].userID != authorID {
		t.Errorf("DM userID: want %d, got %d", authorID, mock.dmCalls[0].userID)
	}

	// Verify DM message contains the block reason
	if len(mock.msgCalls) != 1 {
		t.Fatalf("expected 1 message call, got %d", len(mock.msgCalls))
	}
	if !strings.Contains(mock.msgCalls[0].content, "violence_graphic") {
		t.Errorf("DM message should contain block_reason 'violence_graphic', got: %s", mock.msgCalls[0].content)
	}

	// Verify audit event was emitted
	if len(emitter.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(emitter.events))
	}
	if emitter.events[0].Decision != "BLOCK" {
		t.Errorf("audit decision: want BLOCK, got %s", emitter.events[0].Decision)
	}
	if emitter.events[0].BlockReason != "violence_graphic" {
		t.Errorf("audit block_reason: want violence_graphic, got %s", emitter.events[0].BlockReason)
	}
}

func TestEnforceDecision_ALLOW_noop(t *testing.T) {
	// Scenario: ALLOW decision → no REST calls, no audit event.
	ctx := t.Context()
	channelID, messageID, authorID := testSnowflakes()
	msg := testMessage(channelID, messageID, authorID)

	mock := &mockDiscordRest{}
	emitter := &testEmitter{}

	h := NewWithEnforce(mock, emitter, testLogger(), true)

	err := h.EnforceDecision(ctx, msg, aureliomodv1.Decision_DECISION_ALLOW, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.deleteCalls) != 0 {
		t.Errorf("ALLOW should not delete, got %d delete calls", len(mock.deleteCalls))
	}
	if len(mock.dmCalls) != 0 {
		t.Errorf("ALLOW should not DM, got %d DM calls", len(mock.dmCalls))
	}
	if len(mock.msgCalls) != 0 {
		t.Errorf("ALLOW should not send messages, got %d message calls", len(mock.msgCalls))
	}
	if len(emitter.events) != 0 {
		t.Errorf("ALLOW should not emit audit, got %d events", len(emitter.events))
	}
}

func TestEnforceDecision_QUEUED_fallsBackToBlock(t *testing.T) {
	// Scenario: QUEUED decision → delete message + "pending_analysis" reason.
	ctx := t.Context()
	channelID, messageID, authorID := testSnowflakes()
	msg := testMessage(channelID, messageID, authorID)

	mock := &mockDiscordRest{dmChannel: &discord.DMChannel{}}
	emitter := &testEmitter{}

	h := NewWithEnforce(mock, emitter, testLogger(), true)

	err := h.EnforceDecision(ctx, msg, aureliomodv1.Decision_DECISION_QUEUED, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify delete was called
	if len(mock.deleteCalls) != 1 {
		t.Fatalf("expected 1 delete call for QUEUED, got %d", len(mock.deleteCalls))
	}

	// Verify DM contains "pending_analysis"
	if len(mock.msgCalls) == 0 {
		t.Fatal("QUEUED should send DM with pending_analysis reason")
	}
	if !strings.Contains(mock.msgCalls[0].content, "pending_analysis") {
		t.Errorf("QUEUED DM should contain 'pending_analysis', got: %s", mock.msgCalls[0].content)
	}

	// Verify audit event has QUEUED decision with pending_analysis reason
	if len(emitter.events) != 1 {
		t.Fatalf("expected 1 audit event for QUEUED, got %d", len(emitter.events))
	}
	if emitter.events[0].BlockReason != "pending_analysis" {
		t.Errorf("QUEUED audit block_reason: want pending_analysis, got %s", emitter.events[0].BlockReason)
	}
}

func TestEnforceDecision_gateOff_skipsAll(t *testing.T) {
	// Scenario: ENFORCE_MODERATION=false → skip everything regardless of decision.
	ctx := t.Context()
	channelID, messageID, authorID := testSnowflakes()
	msg := testMessage(channelID, messageID, authorID)

	mock := &mockDiscordRest{}
	emitter := &testEmitter{}

	h := NewWithEnforce(mock, emitter, testLogger(), false)

	err := h.EnforceDecision(ctx, msg, aureliomodv1.Decision_DECISION_BLOCK, "violence_graphic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.deleteCalls) != 0 {
		t.Errorf("gate off: should not delete, got %d calls", len(mock.deleteCalls))
	}
	if len(mock.dmCalls) != 0 {
		t.Errorf("gate off: should not DM, got %d calls", len(mock.dmCalls))
	}
	if len(emitter.events) != 0 {
		t.Errorf("gate off: should not emit audit, got %d events", len(emitter.events))
	}
}

func TestEnforceDecision_BLOCK_dmFailure_stillDeletes(t *testing.T) {
	// Scenario: DM creation fails → still delete the message (resilience).
	ctx := t.Context()
	channelID, messageID, authorID := testSnowflakes()
	msg := testMessage(channelID, messageID, authorID)

	mock := &mockDiscordRest{
		dmErr: errors.New("cannot DM this user"),
	}
	emitter := &testEmitter{}

	h := NewWithEnforce(mock, emitter, testLogger(), true)

	err := h.EnforceDecision(ctx, msg, aureliomodv1.Decision_DECISION_BLOCK, "violence_graphic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Delete must still happen even if DM fails
	if len(mock.deleteCalls) != 1 {
		t.Fatalf("delete should still happen on DM failure, got %d calls", len(mock.deleteCalls))
	}
	// No DM message should be sent (DM channel creation failed)
	if len(mock.msgCalls) != 0 {
		t.Errorf("no DM msg should be sent when CreateDMChannel fails, got %d", len(mock.msgCalls))
	}
	// Audit should still be emitted
	if len(emitter.events) != 1 {
		t.Errorf("audit should be emitted even on DM failure, got %d events", len(emitter.events))
	}
}

func TestAuditEventJSON(t *testing.T) {
	// Verify audit events are valid JSON with required fields.
	ae := audit.AuditEvent{
		WorkspaceID:    "ws_test",
		ContentID:      "cid_123",
		ContentHash:    "abc",
		Decision:       "BLOCK",
		BlockReason:    "violence_graphic",
		Category:       "violence",
		AnalystVersion: "v3.2",
		GuildID:        "11111",
		AuthorID:       "300",
		SourcePlatform: "discord",
		ElapsedMs:      150,
	}

	data, err := json.Marshal(ae)
	if err != nil {
		t.Fatalf("failed to marshal audit event: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal audit event: %v", err)
	}

	requiredFields := []string{
		"workspace_id", "content_id", "content_hash", "decision",
		"block_reason", "guild_id", "author_id", "source_platform", "elapsed_ms",
	}
	for _, field := range requiredFields {
		if _, ok := parsed[field]; !ok {
			t.Errorf("required audit field missing: %s", field)
		}
	}
}
