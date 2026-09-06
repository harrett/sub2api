-- 会话数据留存：线程标识与续跑标记。
--
-- thread_id：Codex 并行派发子代理时多个线程共用一个 session_id（抽样中四条记录
-- session_id 相同，其中三条是三个不同子代理在并行跑），只按 session_id 排序会把
-- 它们串在一起。thread_id 才是"一条线性对话"的正确粒度。
--
-- is_continuation：本次请求只是 agent 循环续跑，用户没有新提问。这类记录的
-- input 与上一条完全相同（抽样中连续三条记录 input 一字不差），风控列表默认应
-- 折叠掉，蒸馏取 (指令, 回答) 样本时也应排除。

ALTER TABLE conversation_capture_index
    ADD COLUMN IF NOT EXISTS thread_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE conversation_capture_index
    ADD COLUMN IF NOT EXISTS is_continuation BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_conversation_capture_thread_created
    ON conversation_capture_index(thread_id, created_at DESC)
    WHERE thread_id <> '';

-- 风控主查询默认只看新提问，因此把续跑标记并进账号维度的复合索引。
CREATE INDEX IF NOT EXISTS idx_conversation_capture_account_new_turns
    ON conversation_capture_index(account_id, created_at DESC)
    WHERE is_continuation = FALSE;
