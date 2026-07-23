import { apiClient } from './client'

export interface BundlePlanGroup {
  group_id: number
  platform?: string
  group_name?: string
  daily_limit_usd?: number | null
  monthly_limit_usd?: number | null
}

export interface BundlePlan {
  id: number
  name: string
  description: string
  product_name: string
  price: number
  original_price?: number | null
  currency: string
  validity_days: number
  validity_unit: string
  shared_daily_limit_usd?: number | null
  shared_monthly_limit_usd?: number | null
  features: string
  for_sale: boolean
  sort_order: number
  groups?: BundlePlanGroup[]
}

export interface BundleEntitlement {
	group_id: number
	platform: string
	daily_limit_usd?: number | null
	monthly_limit_usd?: number | null
	daily_usage_usd: number
	monthly_usage_usd: number
}

export interface BundleSubscription {
  id: number
  user_id: number
  bundle_plan_id: number
  status: 'pending' | 'active' | 'expired' | 'revoked' | string
  starts_at: string
  expires_at: string
  daily_usage_usd: number
  monthly_usage_usd: number
  entitlements: BundleEntitlement[]
  plan?: BundlePlan
}

export const bundleSubscriptionsAPI = {
  getPlans() {
    return apiClient.get<BundlePlan[]>('/bundle-subscriptions/plans')
  },
  getMine() {
    return apiClient.get<BundleSubscription[]>('/bundle-subscriptions')
  },
  cancelPending(id: number) {
    return apiClient.post(`/bundle-subscriptions/${id}/cancel`)
  },
}

export default bundleSubscriptionsAPI
