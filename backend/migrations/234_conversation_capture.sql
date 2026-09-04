-- 会话数据留存（conversation capture）轻量检索索引。
--
-- 完整 prompt / response 只存对象存储（gzip JSONL 段），本表只保留检索所需的
-- 元数据、用户输入预览和定位信息（object_key + request_id），并强制保留期清理，
-- 保证 PostgreSQL 容量有明确上限。

CREATE TABLE IF NOT EXISTS conversation_capture_index (
    id             BIGSERIAL PRIMARY KEY,
    request_id     VARCHAR(128) NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    -- 归属维度（用户删除后置空，不牵连日志行）
    user_id        BIGINT REFERENCES users(id)    ON DELETE SET NULL,
    api_key_id     BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    account_id     BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    group_id       BIGINT REFERENCES groups(id)   ON DELETE SET NULL,

    -- 快照名，避免风控查询联表；关联行被删后仍可读
    user_email     VARCHAR(320) NOT NULL DEFAULT '',
    api_key_name   VARCHAR(255) NOT NULL DEFAULT '',
    account_name   VARCHAR(255) NOT NULL DEFAULT '',
    group_name     VARCHAR(255) NOT NULL DEFAULT '',

    platform       VARCHAR(64)  NOT NULL DEFAULT '',
    protocol       VARCHAR(64)  NOT NULL DEFAULT '',
    endpoint       VARCHAR(128) NOT NULL DEFAULT '',
    model          VARCHAR(255) NOT NULL DEFAULT '',
    stream         BOOLEAN      NOT NULL DEFAULT FALSE,
    status_code    INT          NOT NULL DEFAULT 0,
    duration_ms    INT          NOT NULL DEFAULT 0,
    ip_address     VARCHAR(45)  NOT NULL DEFAULT '',

    -- 用户输入预览：默认前 1KB，配置上限 2KB。完整正文永远不进本表。
    input_preview  TEXT   NOT NULL DEFAULT '',
    input_bytes    BIGINT NOT NULL DEFAULT 0,
    output_bytes   BIGINT NOT NULL DEFAULT 0,
    input_tokens   INT    NOT NULL DEFAULT 0,
    output_tokens  INT    NOT NULL DEFAULT 0,

    -- 全文定位：object_key 指向 gzip JSONL 段，按 request_id 在段内定位单行。
    -- object_key 为空表示磁盘保护状态下只写了索引、正文未落盘。
    object_key     VARCHAR(512) NOT NULL DEFAULT '',

    CONSTRAINT chk_conversation_capture_nonnegative
        CHECK (input_bytes >= 0 AND output_bytes >= 0 AND
               input_tokens >= 0 AND output_tokens >= 0 AND duration_ms >= 0)
);

-- 风控搜索主路径：账号 + 时间范围先收敛，再对少量 preview 做 ILIKE。
CREATE INDEX IF NOT EXISTS idx_conversation_capture_account_created
    ON conversation_capture_index(account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_conversation_capture_user_created
    ON conversation_capture_index(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_conversation_capture_group_created
    ON conversation_capture_index(group_id, created_at DESC);
-- 保留期清理按 created_at 扫描。
CREATE INDEX IF NOT EXISTS idx_conversation_capture_created
    ON conversation_capture_index(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_conversation_capture_request
    ON conversation_capture_index(request_id);

-- 功能默认关闭。
INSERT INTO settings (key, value, updated_at)
VALUES ('conversation_capture_config', '{"enabled":false}', NOW())
ON CONFLICT (key) DO NOTHING;
