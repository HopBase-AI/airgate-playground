package playground

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

type Service struct {
	logger                  *slog.Logger
	db                      *sql.DB
	host                    sdk.Host
	storage                 *ObjectStorage
	maxConversationsPerUser int
}

func clampRenderFee(totalCost, renderFee float64) float64 {
	if math.IsNaN(renderFee) || math.IsInf(renderFee, 0) || renderFee <= 0 || totalCost <= 0 {
		return 0
	}
	if renderFee > totalCost {
		return totalCost
	}
	return renderFee
}

func NewService(logger *slog.Logger, db *sql.DB, host sdk.Host, storage *ObjectStorage, maxConversationsPerUser int) *Service {
	return &Service{
		logger:                  logger,
		db:                      db,
		host:                    host,
		storage:                 storage,
		maxConversationsPerUser: maxConversationsPerUser,
	}
}

// ── Conversation CRUD ──

func (s *Service) CreateConversation(ctx context.Context, userID int, title string, groupID int64, platform, model string) (*Conversation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin conversation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if s.maxConversationsPerUser > 0 {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, conversationLimitLockKey(userID)); err != nil {
			return nil, fmt.Errorf("lock conversation count: %w", err)
		}
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*)
			 FROM playground_conversations
			 WHERE user_id = $1`, userID,
		).Scan(&count); err != nil {
			return nil, fmt.Errorf("count conversations: %w", err)
		}
		if count >= s.maxConversationsPerUser {
			return nil, &conversationLimitError{limit: s.maxConversationsPerUser}
		}
	}

	conv := &Conversation{UserID: userID, Title: title, GroupID: groupID, Platform: platform, Model: model}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO playground_conversations (user_id, title, group_id, platform, model)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, created_at, updated_at`,
		userID, title, groupID, platform, model,
	).Scan(&conv.ID, &conv.CreatedAt, &conv.UpdatedAt); err != nil {
		return nil, fmt.Errorf("insert conversation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit conversation transaction: %w", err)
	}
	return conv, nil
}

func (s *Service) ListConversations(ctx context.Context, userID int) ([]Conversation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, title, group_id, platform, model, created_at, updated_at
		 FROM playground_conversations
		 WHERE user_id = $1
		 ORDER BY updated_at DESC, id DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var convs []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.GroupID, &c.Platform, &c.Model, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		convs = append(convs, c)
	}
	return convs, rows.Err()
}

func (s *Service) GetConversation(ctx context.Context, userID int, convID int64) (*Conversation, error) {
	c := &Conversation{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, title, group_id, platform, model, created_at, updated_at
		 FROM playground_conversations
		 WHERE id = $1 AND user_id = $2`, convID, userID,
	).Scan(&c.ID, &c.UserID, &c.Title, &c.GroupID, &c.Platform, &c.Model, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func (s *Service) UpdateConversation(ctx context.Context, userID int, convID int64, title string, groupID int64, platform, model string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE playground_conversations
		 SET title = $1, group_id = $2, platform = $3, model = $4, updated_at = NOW()
		 WHERE id = $5 AND user_id = $6`,
		title, groupID, platform, model, convID, userID,
	)
	return err
}

func (s *Service) DeleteConversation(ctx context.Context, userID int, convID int64) error {
	assets, err := s.listConversationAssets(ctx, userID, convID)
	if err != nil {
		return fmt.Errorf("query conversation assets: %w", err)
	}
	if err := s.deleteAssetsFromStorage(ctx, assets); err != nil {
		return fmt.Errorf("delete conversation assets: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin conversation delete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		"DELETE FROM playground_assets WHERE user_id = $1 AND conversation_id = $2",
		userID, convID,
	); err != nil {
		return fmt.Errorf("delete conversation asset records: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM playground_conversations WHERE id = $1 AND user_id = $2",
		convID, userID,
	); err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit conversation delete transaction: %w", err)
	}
	return nil
}

// ── Messages ──

func (s *Service) ListMessages(ctx context.Context, userID int, convID int64) ([]Message, error) {
	var ownerID int
	if err := s.db.QueryRowContext(ctx,
		"SELECT user_id FROM playground_conversations WHERE id = $1", convID,
	).Scan(&ownerID); err != nil {
		return nil, err
	}
	if ownerID != userID {
		return nil, fmt.Errorf("conversation not found")
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, role, content, reasoning, reasoning_effort, platform, model, group_id, input_tokens, output_tokens, cost, render_fee, finish_reason, tool_calls, created_at
		 FROM playground_messages
		 WHERE conversation_id = $1
		 ORDER BY created_at`, convID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var msgs []Message
	for rows.Next() {
		var m Message
		var toolCalls []byte
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.Reasoning, &m.ReasoningEffort, &m.Platform, &m.Model, &m.GroupID, &m.InputTokens, &m.OutputTokens, &m.Cost, &m.RenderFee, &m.FinishReason, &toolCalls, &m.CreatedAt); err != nil {
			return nil, err
		}
		if len(toolCalls) > 0 && string(toolCalls) != "[]" {
			m.ToolCalls = json.RawMessage(toolCalls)
			if resolved, err := s.resolveToolCallAssetURLs(ctx, userID, m.ToolCalls); err != nil {
				s.logger.Warn("failed to resolve tool call assets", "error", err, "message_id", m.ID)
			} else {
				m.ToolCalls = resolved
			}
		}
		if resolved, err := s.resolveAssetURLs(ctx, userID, m.Content); err != nil {
			s.logger.Warn("failed to resolve message assets", "error", err, "message_id", m.ID)
		} else {
			m.Content = resolved
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// GetMessageForExport loads one message after verifying the conversation owner.
// The returned content keeps stable asset URIs; export renderers treat them as
// inert text rather than trusting user-supplied URLs.
func (s *Service) GetMessageForExport(ctx context.Context, userID int, messageID int64) (*Message, *Conversation, error) {
	if messageID <= 0 {
		return nil, nil, fmt.Errorf("message_id required")
	}
	var msg Message
	var conv Conversation
	var toolCalls []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT m.id, m.conversation_id, m.role, m.content, m.reasoning, m.reasoning_effort,
		        m.platform, m.model, m.group_id, m.input_tokens, m.output_tokens, m.cost, m.render_fee, m.finish_reason,
		        m.tool_calls, m.created_at,
		        c.id, c.user_id, c.title, c.group_id, c.platform, c.model, c.created_at, c.updated_at
		 FROM playground_messages m
		 JOIN playground_conversations c ON c.id = m.conversation_id
		 WHERE m.id = $1 AND c.user_id = $2`, messageID, userID,
	).Scan(&msg.ID, &msg.ConversationID, &msg.Role, &msg.Content, &msg.Reasoning, &msg.ReasoningEffort,
		&msg.Platform, &msg.Model, &msg.GroupID, &msg.InputTokens, &msg.OutputTokens, &msg.Cost, &msg.RenderFee, &msg.FinishReason,
		&toolCalls, &msg.CreatedAt,
		&conv.ID, &conv.UserID, &conv.Title, &conv.GroupID, &conv.Platform, &conv.Model, &conv.CreatedAt, &conv.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil, fmt.Errorf("message not found")
	}
	if err != nil {
		return nil, nil, err
	}
	if len(toolCalls) > 0 && string(toolCalls) != "[]" {
		msg.ToolCalls = json.RawMessage(toolCalls)
	}
	return &msg, &conv, nil
}

func (s *Service) saveMessage(ctx context.Context, convID int64, role, content, reasoning, reasoningEffort, platform, model, finishReason string, groupID int64, inputTokens, outputTokens int, cost, renderFee float64, toolCalls json.RawMessage) (*Message, error) {
	toolCallsValue := "[]"
	if len(toolCalls) > 0 {
		toolCallsValue = string(toolCalls)
	}
	m := &Message{
		ConversationID:  convID,
		Role:            role,
		Content:         content,
		Reasoning:       reasoning,
		ReasoningEffort: reasoningEffort,
		Platform:        platform,
		Model:           model,
		GroupID:         groupID,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		Cost:            cost,
		RenderFee:       renderFee,
		FinishReason:    finishReason,
		ToolCalls:       toolCalls,
	}
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO playground_messages (conversation_id, role, content, reasoning, reasoning_effort, platform, model, finish_reason, group_id, input_tokens, output_tokens, cost, render_fee, tool_calls)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 RETURNING id, created_at`,
		convID, role, content, reasoning, reasoningEffort, platform, model, finishReason, groupID, inputTokens, outputTokens, cost, renderFee, toolCallsValue,
	).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		return nil, err
	}

	if _, err := s.db.ExecContext(ctx, "UPDATE playground_conversations SET updated_at = NOW() WHERE id = $1", convID); err != nil {
		s.logger.Error("failed to refresh conversation timestamp", "error", err, "conv_id", convID)
	}
	return m, nil
}

type PersistMessageRequest struct {
	ConversationID  int64   `json:"conversation_id"`
	Role            string  `json:"role"`
	Content         string  `json:"content"`
	Reasoning       string  `json:"reasoning"`
	ReasoningEffort string  `json:"reasoning_effort"`
	Platform        string  `json:"platform"`
	Model           string  `json:"model"`
	GroupID         int64   `json:"group_id"`
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	Cost            float64 `json:"cost"`
	RenderFee       float64 `json:"render_fee"`
	FinishReason    string  `json:"finish_reason"`
	// ToolCalls 工具循环时间线(前端把 SSE 收到的事件原样回传,≤64KB JSON 数组)。
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

const maxToolCallsPersistBytes = 64 << 10

type UpdateMessageRequest struct {
	Content      string  `json:"content"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"`
	RenderFee    float64 `json:"render_fee"`
}

func (s *Service) UpdateMessage(ctx context.Context, userID int, msgID int64, req UpdateMessageRequest) (*Message, error) {
	if msgID <= 0 {
		return nil, fmt.Errorf("message_id required")
	}

	var msg Message
	var convUserID int
	if err := s.db.QueryRowContext(ctx,
		`SELECT m.id, m.conversation_id, m.role, m.content, m.reasoning, m.reasoning_effort, m.platform, m.model, m.group_id, m.input_tokens, m.output_tokens, m.cost, m.render_fee, m.finish_reason, m.created_at, c.user_id
		 FROM playground_messages m
		 JOIN playground_conversations c ON c.id = m.conversation_id
		 WHERE m.id = $1`, msgID,
	).Scan(&msg.ID, &msg.ConversationID, &msg.Role, &msg.Content, &msg.Reasoning, &msg.ReasoningEffort, &msg.Platform, &msg.Model, &msg.GroupID, &msg.InputTokens, &msg.OutputTokens, &msg.Cost, &msg.RenderFee, &msg.FinishReason, &msg.CreatedAt, &convUserID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("message not found")
		}
		return nil, fmt.Errorf("get message: %w", err)
	}
	if convUserID != userID {
		return nil, fmt.Errorf("message not found")
	}

	content, err := s.storeContentAssets(ctx, userID, msg.ConversationID, req.Content)
	if err != nil {
		return nil, fmt.Errorf("store message assets: %w", err)
	}
	msg.Content = content
	msg.InputTokens += req.InputTokens
	msg.OutputTokens += req.OutputTokens
	msg.Cost += req.Cost
	msg.RenderFee += req.RenderFee
	msg.RenderFee = clampRenderFee(msg.Cost, msg.RenderFee)

	if _, err := s.db.ExecContext(ctx,
		`UPDATE playground_messages
		 SET content = $1, input_tokens = $2, output_tokens = $3, cost = $4, render_fee = $5
		 WHERE id = $6`,
		msg.Content, msg.InputTokens, msg.OutputTokens, msg.Cost, msg.RenderFee, msg.ID,
	); err != nil {
		return nil, fmt.Errorf("update message: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE playground_conversations SET updated_at = NOW() WHERE id = $1", msg.ConversationID); err != nil {
		s.logger.Error("failed to refresh conversation timestamp", "error", err, "conv_id", msg.ConversationID)
	}
	if resolved, err := s.resolveAssetURLs(ctx, userID, msg.Content); err != nil {
		s.logger.Warn("failed to resolve updated message assets", "error", err, "message_id", msg.ID)
	} else {
		msg.Content = resolved
	}
	return &msg, nil
}

func (s *Service) PersistMessage(ctx context.Context, userID int, req PersistMessageRequest) (*Message, error) {
	if req.ConversationID <= 0 {
		return nil, fmt.Errorf("conversation_id required")
	}
	if req.Role != "user" && req.Role != "assistant" && req.Role != "system" {
		return nil, fmt.Errorf("invalid role")
	}

	conv, err := s.GetConversation(ctx, userID, req.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	if conv == nil {
		return nil, fmt.Errorf("conversation not found")
	}

	groupID := req.GroupID
	if groupID <= 0 {
		groupID = conv.GroupID
	}
	platform := req.Platform
	if platform == "" {
		platform = conv.Platform
	}
	model := req.Model
	if model == "" {
		model = conv.Model
	}

	content, err := s.storeContentAssets(ctx, userID, conv.ID, req.Content)
	if err != nil {
		return nil, fmt.Errorf("store message assets: %w", err)
	}

	toolCalls := req.ToolCalls
	if len(toolCalls) > 0 {
		var arr []json.RawMessage
		if len(toolCalls) > maxToolCallsPersistBytes || json.Unmarshal(toolCalls, &arr) != nil {
			s.logger.Warn("persist_tool_calls_rejected", "bytes", len(toolCalls))
			toolCalls = nil
		}
	}

	renderFee := clampRenderFee(req.Cost, req.RenderFee)
	msg, err := s.saveMessage(ctx, conv.ID, req.Role, content, req.Reasoning, req.ReasoningEffort, platform, model, req.FinishReason, groupID, req.InputTokens, req.OutputTokens, req.Cost, renderFee, toolCalls)
	if err != nil {
		return nil, fmt.Errorf("save message: %w", err)
	}
	if resolved, err := s.resolveAssetURLs(ctx, userID, msg.Content); err != nil {
		s.logger.Warn("failed to resolve persisted message assets", "error", err, "message_id", msg.ID)
	} else {
		msg.Content = resolved
	}
	if len(msg.ToolCalls) > 0 {
		if resolved, err := s.resolveToolCallAssetURLs(ctx, userID, msg.ToolCalls); err != nil {
			s.logger.Warn("failed to resolve persisted tool call assets", "error", err, "message_id", msg.ID)
		} else {
			msg.ToolCalls = resolved
		}
	}

	if conv.Title == "" && req.Role == "assistant" {
		titleSeed, titleErr := s.latestUserMessageContent(ctx, conv.ID)
		if titleErr != nil {
			s.logger.Error("failed to query title seed", "error", titleErr, "conv_id", conv.ID)
		} else if titleSeed != "" {
			if err := s.UpdateConversation(ctx, userID, conv.ID, generateTitle(titleSeed), conv.GroupID, conv.Platform, conv.Model); err != nil {
				s.logger.Error("failed to update conversation title", "error", err, "conv_id", conv.ID)
			}
		}
	}

	return msg, nil
}

// Storage 暴露对象存储(工具循环的文档生成用)。
func (s *Service) Storage() *ObjectStorage {
	return s.storage
}

// RegisterConversationAsset 把工具产物登记进 playground_assets(带会话归属,
// 纳入孤儿清理与会话删除链路)。
func (s *Service) RegisterConversationAsset(ctx context.Context, userID int, convID int64, asset *StoredAsset) error {
	return s.insertAsset(ctx, userID, convID, asset)
}

// RemoveConversationAsset removes both the persisted ownership row and the
// backing object. It is used when a post-render side effect (such as a strict
// usage charge) fails after the asset was already registered.
func (s *Service) RemoveConversationAsset(ctx context.Context, userID int, convID int64, asset *StoredAsset) error {
	if asset == nil || asset.ID == "" {
		return fmt.Errorf("asset required")
	}
	// Remove the backing object first. If storage is temporarily unavailable,
	// keep the ownership row so a later cleanup/retry still has the object key.
	if s.storage != nil && asset.ObjectKey != "" {
		if err := s.storage.Delete(ctx, asset.ObjectKey); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM playground_assets WHERE id = $1 AND user_id = $2 AND conversation_id = $3`,
		asset.ID, userID, convID,
	); err != nil {
		return err
	}
	return nil
}

// InsertToolCallAudit 工具执行审计入库(调用方负责失败降级)。
func (s *Service) InsertToolCallAudit(ctx context.Context, rec toolCallAudit) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO playground_tool_calls (user_id, conversation_id, request_id, iteration, tool_name, arguments, status, error, duration_ms, result_bytes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		rec.UserID, rec.ConversationID, rec.RequestID, rec.Iteration, rec.ToolName, rec.Arguments, rec.Status, rec.Error, rec.DurationMs, rec.ResultBytes,
	)
	return err
}

// TruncateMessages 线性截断:删除会话内 afterMessageID 之后的所有消息
// (含边界外校验),供前端「编辑重发」使用。返回删除条数。
func (s *Service) TruncateMessages(ctx context.Context, userID int, convID, afterMessageID int64) (int64, error) {
	if convID <= 0 || afterMessageID <= 0 {
		return 0, fmt.Errorf("conversation_id and after_message_id required")
	}
	conv, err := s.GetConversation(ctx, userID, convID)
	if err != nil || conv == nil {
		return 0, fmt.Errorf("conversation not found")
	}
	var anchor int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT id FROM playground_messages WHERE id = $1 AND conversation_id = $2", afterMessageID, convID,
	).Scan(&anchor); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("message not found")
		}
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM playground_messages WHERE conversation_id = $1 AND id > $2", convID, afterMessageID,
	)
	if err != nil {
		return 0, err
	}
	deleted, _ := res.RowsAffected()
	if _, err := s.db.ExecContext(ctx, "UPDATE playground_conversations SET updated_at = NOW() WHERE id = $1", convID); err != nil {
		s.logger.Error("failed to refresh conversation timestamp", "error", err, "conv_id", convID)
	}
	return deleted, nil
}

func (s *Service) storeContentAssets(ctx context.Context, userID int, convID int64, content string) (string, error) {
	if s.storage == nil || (!strings.Contains(content, "data:image/") && !strings.Contains(content, "data:video/")) {
		return content, nil
	}
	var firstErr error
	stored := dataImageURLRE.ReplaceAllStringFunc(content, func(dataURL string) string {
		asset, err := s.storage.StoreImageDataURL(ctx, userID, convID, dataURL)
		if err != nil {
			firstErr = err
			return dataURL
		}
		if err := s.insertAsset(ctx, userID, convID, asset); err != nil {
			firstErr = err
			return dataURL
		}
		return assetURI(asset.ID)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return stored, nil
}

func (s *Service) listConversationAssets(ctx context.Context, userID int, convID int64) ([]Asset, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, conversation_id, object_key, content_type, size_bytes, created_at
		 FROM playground_assets
		 WHERE user_id = $1 AND conversation_id = $2
		 ORDER BY created_at ASC, id ASC`, userID, convID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var assets []Asset
	for rows.Next() {
		var asset Asset
		if err := rows.Scan(&asset.ID, &asset.UserID, &asset.ConversationID, &asset.ObjectKey, &asset.ContentType, &asset.SizeBytes, &asset.CreatedAt); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func (s *Service) listOrphanAssets(ctx context.Context, limit int) ([]Asset, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.user_id, a.conversation_id, a.object_key, a.content_type, a.size_bytes, a.created_at
		 FROM playground_assets a
		 LEFT JOIN playground_conversations c
		   ON c.id = a.conversation_id AND c.user_id = a.user_id
		 WHERE c.id IS NULL
		 ORDER BY a.created_at ASC, a.id ASC
		 LIMIT $1`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var assets []Asset
	for rows.Next() {
		var asset Asset
		if err := rows.Scan(&asset.ID, &asset.UserID, &asset.ConversationID, &asset.ObjectKey, &asset.ContentType, &asset.SizeBytes, &asset.CreatedAt); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func (s *Service) deleteAssetsFromStorage(ctx context.Context, assets []Asset) error {
	if s.storage == nil || len(assets) == 0 {
		return nil
	}
	var combinedErr error
	for _, asset := range assets {
		if asset.ObjectKey == "" {
			continue
		}
		if err := s.storage.Delete(ctx, asset.ObjectKey); err != nil {
			if s.logger != nil {
				s.logger.Warn("删除 Playground 资产失败",
					"asset_id", asset.ID,
					"object_key", asset.ObjectKey,
					"error", err)
			}
			combinedErr = errors.Join(combinedErr, err)
		}
	}
	return combinedErr
}

func (s *Service) CleanupOrphanAssets(ctx context.Context, limit int) (int, error) {
	assets, err := s.listOrphanAssets(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("query orphan assets: %w", err)
	}
	deleted := 0
	var combinedErr error
	for _, asset := range assets {
		if err := s.deleteAssetsFromStorage(ctx, []Asset{asset}); err != nil {
			combinedErr = errors.Join(combinedErr, err)
			continue
		}
		if _, err := s.db.ExecContext(ctx, "DELETE FROM playground_assets WHERE id = $1", asset.ID); err != nil {
			combinedErr = errors.Join(combinedErr, err)
			continue
		}
		deleted++
	}
	return deleted, combinedErr
}

func (s *Service) insertAsset(ctx context.Context, userID int, convID int64, asset *StoredAsset) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO playground_assets (id, user_id, conversation_id, object_key, content_type, size_bytes)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		asset.ID, userID, convID, asset.ObjectKey, asset.ContentType, asset.SizeBytes,
	)
	return err
}

func (s *Service) resolveAssetURLs(ctx context.Context, userID int, content string) (string, error) {
	if s.storage == nil || !strings.Contains(content, "airgate-asset://") {
		return content, nil
	}
	var firstErr error
	assetLinkRE := regexp.MustCompile(`airgate-asset://asset/[A-Za-z0-9]+`)
	resolved := assetLinkRE.ReplaceAllStringFunc(content, func(raw string) string {
		id, ok := parseAssetURI(raw)
		if !ok {
			return raw
		}
		asset, err := s.getAsset(ctx, userID, id)
		if err != nil {
			firstErr = err
			return raw
		}
		publicURL, err := s.storage.PublicURL(ctx, asset.ObjectKey)
		if err != nil {
			firstErr = err
			return raw
		}
		return publicURL
	})
	return resolved, firstErr
}

// resolveToolCallAssetURLs refreshes expiring download URLs in persisted file
// cards. The database keeps asset_uri as the stable reference; src is replaced
// only in the API response after verifying that the asset belongs to the user.
func (s *Service) resolveToolCallAssetURLs(ctx context.Context, userID int, toolCalls json.RawMessage) (json.RawMessage, error) {
	if s.storage == nil || !bytes.Contains(toolCalls, []byte("airgate-asset://")) {
		return toolCalls, nil
	}
	return refreshToolCallAssetURLs(toolCalls, func(raw string) (string, error) {
		id, ok := parseAssetURI(raw)
		if !ok {
			return "", fmt.Errorf("invalid tool call asset URI")
		}
		asset, err := s.getAsset(ctx, userID, id)
		if err != nil {
			return "", err
		}
		return s.storage.PublicURL(ctx, asset.ObjectKey)
	})
}

func refreshToolCallAssetURLs(toolCalls json.RawMessage, resolve func(string) (string, error)) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(toolCalls, &value); err != nil {
		return toolCalls, err
	}
	var firstErr error
	var visit func(any)
	visit = func(node any) {
		switch current := node.(type) {
		case map[string]any:
			if raw, ok := current["asset_uri"].(string); ok && strings.HasPrefix(raw, "airgate-asset://") {
				publicURL, err := resolve(raw)
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
				} else {
					current["src"] = publicURL
				}
			}
			for _, child := range current {
				visit(child)
			}
		case []any:
			for _, child := range current {
				visit(child)
			}
		}
	}
	visit(value)
	resolved, err := json.Marshal(value)
	if err != nil {
		return toolCalls, err
	}
	return resolved, firstErr
}

func (s *Service) getAsset(ctx context.Context, userID int, id string) (*Asset, error) {
	asset := &Asset{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, conversation_id, object_key, content_type, size_bytes, created_at
		 FROM playground_assets
		 WHERE id = $1 AND user_id = $2`, id, userID,
	).Scan(&asset.ID, &asset.UserID, &asset.ConversationID, &asset.ObjectKey, &asset.ContentType, &asset.SizeBytes, &asset.CreatedAt)
	return asset, err
}

func (s *Service) latestUserMessageContent(ctx context.Context, convID int64) (string, error) {
	var content string
	err := s.db.QueryRowContext(ctx,
		`SELECT content FROM playground_messages
		 WHERE conversation_id = $1 AND role = 'user'
		 ORDER BY created_at DESC
		 LIMIT 1`, convID,
	).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(content), nil
}

func generateTitle(userMessage string) string {
	runes := []rune(userMessage)
	if len(runes) > 30 {
		return string(runes[:30]) + "..."
	}
	return userMessage
}

type conversationLimitError struct {
	limit int
}

func (e *conversationLimitError) Error() string {
	return fmt.Sprintf("Conversation limit reached (%d). Delete an old conversation before creating a new one.", e.limit)
}

var conversationLimitLockNamespace int64 = 0x41475250

func conversationLimitLockKey(userID int) int64 {
	return conversationLimitLockNamespace<<32 | int64(uint32(userID))
}
