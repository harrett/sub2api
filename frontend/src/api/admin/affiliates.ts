/**
 * Admin Affiliate API endpoints
 * Manage per-user affiliate (邀请返利) configurations:
 * exclusive invite codes (overrides aff_code) and exclusive rebate rates.
 */

import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'

export interface AffiliateAdminEntry {
  user_id: number
  email: string
  username: string
  aff_code: string
  aff_code_custom: boolean
  aff_rebate_rate_percent?: number | null
  aff_count: number
}

export interface ListAffiliateUsersParams {
  page?: number
  page_size?: number
  search?: string
}

export interface ListAffiliateRecordsParams {
  page?: number
  page_size?: number
  search?: string
  start_at?: string
  end_at?: string
  sort_by?: string
  sort_order?: 'asc' | 'desc'
  timezone?: string
}

export interface AffiliateInviteRecord {
  inviter_id: number
  inviter_email: string
  inviter_username: string
  invitee_id: number
  invitee_email: string
  invitee_username: string
  aff_code: string
  total_rebate: number
  created_at: string
}

export interface AffiliateRebateRecord {
  order_id: number
  out_trade_no: string
  inviter_id: number
  inviter_email: string
  inviter_username: string
  invitee_id: number
  invitee_email: string
  invitee_username: string
  order_amount: number
  pay_amount: number
  rebate_amount: number
  payment_type: string
  order_status: string
  created_at: string
}

export interface AffiliateTransferRecord {
  ledger_id: number
  user_id: number
  user_email: string
  username: string
  amount: number
  balance_after?: number | null
  available_quota_after?: number | null
  frozen_quota_after?: number | null
  history_quota_after?: number | null
  snapshot_available: boolean
  created_at: string
}

export interface ListAffiliateLeaderboardParams {
  page?: number
  page_size?: number
  start_at?: string
  end_at?: string
  sort_by?: 'invite_count' | 'rebate_amount'
  timezone?: string
}

export interface AffiliateLeaderboardEntry {
  inviter_id: number
  inviter_email: string
  inviter_username: string
  aff_code: string
  invite_count: number
  total_rebate: number
  last_invited_at?: string | null
}

export interface GetAffiliateInviteTimelineParams {
  inviter_id?: number
  start_at?: string
  end_at?: string
  granularity?: 'day' | 'week' | 'month'
  timezone?: string
}

export interface AffiliateInviteTimelinePoint {
  bucket: string
  invite_count: number
}

export type AffiliateTopHalfMode = 'headcount' | 'volume'

export interface GetAffiliateTopHalfParams {
  start_at?: string
  end_at?: string
  mode?: AffiliateTopHalfMode
  limit?: number
  timezone?: string
}

export interface AffiliateTopHalfInviter {
  rank: number
  inviter_id: number
  inviter_email: string
  inviter_username: string
  aff_code: string
  invite_count: number
  total_rebate: number
  last_invited_at?: string | null
}

export interface AffiliateTopHalfSummary {
  mode: AffiliateTopHalfMode
  total_inviter_count: number
  top_half_count: number
  total_invite_count: number
  top_half_invite_count: number
  top_half_invite_percent: number
  items: AffiliateTopHalfInviter[]
}

export interface AffiliateUserOverview {
  user_id: number
  email: string
  username: string
  aff_code: string
  rebate_rate_percent: number
  invited_count: number
  rebated_invitee_count: number
  available_quota: number
  history_quota: number
}

export interface UpdateAffiliateUserRequest {
  aff_code?: string
  aff_rebate_rate_percent?: number | null
  /** Set true to explicitly clear the per-user rate (sets it to NULL). */
  clear_rebate_rate?: boolean
}

export interface BatchSetRateRequest {
  user_ids: number[]
  aff_rebate_rate_percent?: number | null
  /** Set true to clear rates instead of setting. */
  clear?: boolean
}

export interface SimpleUser {
  id: number
  email: string
  username: string
}

export async function listUsers(
  params: ListAffiliateUsersParams = {},
): Promise<PaginatedResponse<AffiliateAdminEntry>> {
  const { data } = await apiClient.get<PaginatedResponse<AffiliateAdminEntry>>(
    '/admin/affiliates/users',
    {
      params: {
        page: params.page ?? 1,
        page_size: params.page_size ?? 20,
        search: params.search ?? '',
      },
    },
  )
  return data
}

export async function lookupUsers(q: string): Promise<SimpleUser[]> {
  const { data } = await apiClient.get<SimpleUser[]>(
    '/admin/affiliates/users/lookup',
    { params: { q } },
  )
  return data
}

export async function updateUserSettings(
  userId: number,
  payload: UpdateAffiliateUserRequest,
): Promise<{ user_id: number }> {
  const { data } = await apiClient.put<{ user_id: number }>(
    `/admin/affiliates/users/${userId}`,
    payload,
  )
  return data
}

export async function clearUserSettings(
  userId: number,
): Promise<{ user_id: number }> {
  const { data } = await apiClient.delete<{ user_id: number }>(
    `/admin/affiliates/users/${userId}`,
  )
  return data
}

export async function batchSetRate(
  payload: BatchSetRateRequest,
): Promise<{ affected: number }> {
  const { data } = await apiClient.post<{ affected: number }>(
    '/admin/affiliates/users/batch-rate',
    payload,
  )
  return data
}

function recordParams(params: ListAffiliateRecordsParams = {}) {
  return {
    page: params.page ?? 1,
    page_size: params.page_size ?? 20,
    search: params.search ?? '',
    start_at: params.start_at || undefined,
    end_at: params.end_at || undefined,
    sort_by: params.sort_by || undefined,
    sort_order: params.sort_order || undefined,
    timezone: params.timezone || undefined,
  }
}

export async function listInviteRecords(
  params: ListAffiliateRecordsParams = {},
): Promise<PaginatedResponse<AffiliateInviteRecord>> {
  const { data } = await apiClient.get<PaginatedResponse<AffiliateInviteRecord>>(
    '/admin/affiliates/invites',
    { params: recordParams(params) },
  )
  return data
}

export async function listRebateRecords(
  params: ListAffiliateRecordsParams = {},
): Promise<PaginatedResponse<AffiliateRebateRecord>> {
  const { data } = await apiClient.get<PaginatedResponse<AffiliateRebateRecord>>(
    '/admin/affiliates/rebates',
    { params: recordParams(params) },
  )
  return data
}

export async function listTransferRecords(
  params: ListAffiliateRecordsParams = {},
): Promise<PaginatedResponse<AffiliateTransferRecord>> {
  const { data } = await apiClient.get<PaginatedResponse<AffiliateTransferRecord>>(
    '/admin/affiliates/transfers',
    { params: recordParams(params) },
  )
  return data
}

export async function getLeaderboard(
  params: ListAffiliateLeaderboardParams = {},
): Promise<PaginatedResponse<AffiliateLeaderboardEntry>> {
  const { data } = await apiClient.get<PaginatedResponse<AffiliateLeaderboardEntry>>(
    '/admin/affiliates/stats/leaderboard',
    {
      params: {
        page: params.page ?? 1,
        page_size: params.page_size ?? 20,
        start_at: params.start_at || undefined,
        end_at: params.end_at || undefined,
        sort_by: params.sort_by || undefined,
        timezone: params.timezone || undefined,
      },
    },
  )
  return data
}

export async function getInviteTimeline(
  params: GetAffiliateInviteTimelineParams = {},
): Promise<AffiliateInviteTimelinePoint[]> {
  const { data } = await apiClient.get<AffiliateInviteTimelinePoint[]>(
    '/admin/affiliates/stats/timeline',
    {
      params: {
        inviter_id: params.inviter_id || undefined,
        start_at: params.start_at || undefined,
        end_at: params.end_at || undefined,
        granularity: params.granularity || undefined,
        timezone: params.timezone || undefined,
      },
    },
  )
  return data
}

export async function getTopHalfInviters(
  params: GetAffiliateTopHalfParams = {},
): Promise<AffiliateTopHalfSummary> {
  const { data } = await apiClient.get<AffiliateTopHalfSummary>(
    '/admin/affiliates/stats/top-half',
    {
      params: {
        start_at: params.start_at || undefined,
        end_at: params.end_at || undefined,
        mode: params.mode || undefined,
        limit: params.limit || undefined,
        timezone: params.timezone || undefined,
      },
    },
  )
  return data
}

export async function getUserOverview(
  userId: number,
): Promise<AffiliateUserOverview> {
  const { data } = await apiClient.get<AffiliateUserOverview>(
    `/admin/affiliates/users/${userId}/overview`,
  )
  return data
}

export const affiliatesAPI = {
  listUsers,
  lookupUsers,
  updateUserSettings,
  clearUserSettings,
  batchSetRate,
  listInviteRecords,
  listRebateRecords,
  listTransferRecords,
  getLeaderboard,
  getInviteTimeline,
  getTopHalfInviters,
  getUserOverview,
}

export default affiliatesAPI
