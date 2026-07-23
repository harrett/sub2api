import { apiClient } from '../client'
import type { BundlePlan, BundleSubscription } from '../bundleSubscriptions'

export interface BundlePlanInput {
  name: string
  description?: string
  product_name?: string
  price: number
  original_price?: number | null
  currency?: string
  validity_days: number
  validity_unit?: string
  shared_daily_limit_usd?: number | null
  shared_monthly_limit_usd?: number | null
  features?: string
  for_sale?: boolean
  sort_order?: number
  groups: Array<{ group_id: number; daily_limit_usd?: number | null; monthly_limit_usd?: number | null }>
}

export const adminBundleSubscriptionsAPI = {
  getPlans() { return apiClient.get<BundlePlan[]>('/admin/bundle-subscriptions/plans') },
  createPlan(data: BundlePlanInput) { return apiClient.post<BundlePlan>('/admin/bundle-subscriptions/plans', data) },
  updatePlan(id: number, data: Partial<BundlePlanInput>) { return apiClient.put<BundlePlan>(`/admin/bundle-subscriptions/plans/${id}`, data) },
  deletePlan(id: number) { return apiClient.delete(`/admin/bundle-subscriptions/plans/${id}`) },
  getUserSubscriptions(userId?: number) { return apiClient.get<BundleSubscription[]>('/admin/bundle-subscriptions', { params: userId ? { user_id: userId } : undefined }) },
  assign(userId: number, planId: number, days?: number, notes?: string) { return apiClient.post<BundleSubscription>('/admin/bundle-subscriptions/assign', { user_id: userId, plan_id: planId, days, notes }) },
  cancelPending(id: number, userId: number) { return apiClient.post(`/admin/bundle-subscriptions/${id}/cancel`, { user_id: userId }) },
  extend(id: number, days: number) { return apiClient.post<BundleSubscription>(`/admin/bundle-subscriptions/${id}/extend`, { days }) },
  resetUsage(id: number) { return apiClient.post(`/admin/bundle-subscriptions/${id}/reset-usage`) },
  revoke(id: number) { return apiClient.post(`/admin/bundle-subscriptions/${id}/revoke`) },
}

export default adminBundleSubscriptionsAPI
