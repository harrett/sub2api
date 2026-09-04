import { apiClient } from '../client'

export interface ConversationCaptureSettings {
  enabled: boolean
  reuse_backup_s3: boolean
  sample_rate: number
  excluded_group_ids: number[]
  bucket: string
  prefix: string
  endpoint: string
  region: string
  access_key_id: string
  secret_access_key?: string
  force_path_style: boolean
  queue_capacity: number
  queue_max_bytes: number
  max_request_bytes: number
  max_response_bytes: number
  preview_bytes: number
  rotate_bytes: number
  rotate_interval_seconds: number
  spool_max_bytes: number
  disk_min_free_bytes: number
  disk_critical_free_bytes: number
  index_retention_days: number
  secret_configured?: boolean
}

export interface ConversationCaptureRuntime {
  enabled: boolean
  degraded: boolean
  degraded_reason?: string
  queue_depth: number
  queue_capacity: number
  queue_bytes: number
  queue_max_bytes: number
  dropped_total: number
  captured_total: number
  spooled_total: number
  spool_bytes: number
  spool_max_bytes: number
  disk_free_bytes: number
  pending_uploads: number
  uploaded_total: number
  upload_failed_total: number
  index_write_failed_total: number
  last_error?: string
  object_store_enabled: boolean
}

export interface ConversationCaptureRecord {
  id: number
  request_id: string
  created_at: string
  user_id?: number
  api_key_id?: number
  account_id?: number
  group_id?: number
  user_email: string
  api_key_name: string
  account_name: string
  group_name: string
  platform: string
  protocol: string
  endpoint: string
  model: string
  stream: boolean
  status_code: number
  duration_ms: number
  ip_address: string
  input_preview: string
  input_bytes: number
  output_bytes: number
  input_tokens: number
  output_tokens: number
  object_key: string
}

export interface ConversationCaptureSummary {
  total: number
  user_count: number
  input_bytes: number
  output_bytes: number
}

export interface ConversationCaptureSearchResult {
  records: ConversationCaptureRecord[]
  summary?: ConversationCaptureSummary
  filter: {
    account_id: number
    start: string
    end: string
    keyword?: string
    limit: number
  }
}

/** 后端强制：account_id 与时间范围必填，跨度上限 30 天，limit 上限 200。 */
export interface ConversationCaptureSearchParams {
  account_id: number
  start?: string
  end?: string
  keyword?: string
  user_id?: number
  limit?: number
}

const BASE = '/admin/conversation-capture'

export async function getCaptureConfig(): Promise<ConversationCaptureSettings> {
  const { data } = await apiClient.get<ConversationCaptureSettings>(`${BASE}/config`)
  return data
}

export async function updateCaptureConfig(
  config: ConversationCaptureSettings,
): Promise<ConversationCaptureSettings> {
  const { data } = await apiClient.put<ConversationCaptureSettings>(`${BASE}/config`, config)
  return data
}

export async function testCaptureConnection(
  config: ConversationCaptureSettings,
): Promise<{ ok: boolean }> {
  const { data } = await apiClient.post<{ ok: boolean }>(`${BASE}/config/test`, config)
  return data
}

export async function getCaptureRuntime(): Promise<ConversationCaptureRuntime> {
  const { data } = await apiClient.get<ConversationCaptureRuntime>(`${BASE}/runtime`)
  return data
}

export async function searchCaptureRecords(
  params: ConversationCaptureSearchParams,
): Promise<ConversationCaptureSearchResult> {
  const { data } = await apiClient.get<ConversationCaptureSearchResult>(`${BASE}/records`, {
    params,
  })
  return data
}

export async function getCaptureRecordFull(
  requestId: string,
): Promise<{ request_id: string; record: unknown }> {
  const { data } = await apiClient.get<{ request_id: string; record: unknown }>(
    `${BASE}/records/${encodeURIComponent(requestId)}/full`,
  )
  return data
}

export const conversationCaptureAPI = {
  getCaptureConfig,
  updateCaptureConfig,
  testCaptureConnection,
  getCaptureRuntime,
  searchCaptureRecords,
  getCaptureRecordFull,
}

export default conversationCaptureAPI
