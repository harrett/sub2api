import { computed, type ComputedRef } from 'vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

/**
 * Whether the current user should see group "xN 倍率" rate labels on
 * self-service pages (keys, subscriptions, purchase, available channels,
 * model plaza). Controlled by the admin's "隐藏分组倍率显示" system setting;
 * admins always see the rate regardless of the setting.
 */
export function useShowRateMultiplier(): ComputedRef<boolean> {
  const appStore = useAppStore()
  const authStore = useAuthStore()
  return computed(() => authStore.isAdmin || !appStore.cachedPublicSettings?.hide_key_rate_multiplier)
}
