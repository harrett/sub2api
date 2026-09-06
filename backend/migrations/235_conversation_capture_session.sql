-- 会话数据留存：补上会话标识。
--
-- 单条记录只保留本轮的用户输入与模型输出，上下文靠"同一用户的相邻记录"重建。
-- 只按 user_id + 时间排序在并发会话下会串味（Codex 会为一个用户同时开多个
-- subagent 线程），因此把客户端提供的会话标识一并落库，能拿到就按它精确分组。

ALTER TABLE conversation_capture_index
    ADD COLUMN IF NOT EXISTS session_id VARCHAR(255) NOT NULL DEFAULT '';

-- 部分索引：多数客户端不提供会话标识，空串行没有建索引的价值。
CREATE INDEX IF NOT EXISTS idx_conversation_capture_session_created
    ON conversation_capture_index(session_id, created_at DESC)
    WHERE session_id <> '';
