<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Loading State -->
      <div v-if="loading" class="flex justify-center py-12">
        <div
          class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"
        ></div>
      </div>

      <section v-if="bundleEnabled && bundleSubscriptions.length > 0" class="space-y-3">
        <div class="flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">Cross-platform contracts</h2>
          <button v-if="paymentEnabled" class="btn btn-primary text-sm" @click="router.push('/purchase')">Buy or extend</button>
        </div>
        <div class="grid gap-4 lg:grid-cols-2">
          <div v-for="subscription in bundleSubscriptions" :key="subscription.id" class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
            <div class="flex items-center justify-between gap-3">
              <div>
                <h3 class="font-semibold text-gray-900 dark:text-white">{{ subscription.plan?.name || `Bundle #${subscription.bundle_plan_id}` }}</h3>
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ subscription.starts_at }} - {{ subscription.expires_at }}</p>
              </div>
              <span class="badge" :class="subscription.status === 'active' ? 'badge-success' : 'badge-warning'">{{ subscription.status }}</span>
            </div>
            <div class="mt-4 grid grid-cols-2 gap-3 text-sm">
              <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-900"><span class="block text-xs text-gray-500">Daily</span><strong>${{ subscription.daily_usage_usd.toFixed(2) }}</strong><span v-if="subscription.plan?.shared_daily_limit_usd != null" class="text-gray-500"> / ${{ subscription.plan.shared_daily_limit_usd.toFixed(2) }}</span></div>
              <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-900"><span class="block text-xs text-gray-500">Monthly</span><strong>${{ subscription.monthly_usage_usd.toFixed(2) }}</strong><span v-if="subscription.plan?.shared_monthly_limit_usd != null" class="text-gray-500"> / ${{ subscription.plan.shared_monthly_limit_usd.toFixed(2) }}</span></div>
            </div>
            <button v-if="subscription.status === 'pending'" class="btn btn-secondary mt-3 w-full text-sm" @click="cancelBundle(subscription.id)">Cancel pending change</button>
          </div>
        </div>
      </section>

      <!-- Empty State -->
      <div v-else-if="subscriptions.length === 0 && (!bundleEnabled || bundleSubscriptions.length === 0)" class="card p-12 text-center">
        <div
          class="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-gray-100 dark:bg-dark-700"
        >
          <Icon name="creditCard" size="xl" class="text-gray-400" />
        </div>
        <h3 class="mb-2 text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('userSubscriptions.noActiveSubscriptions') }}
        </h3>
        <p class="text-gray-500 dark:text-dark-400">
          {{ t('userSubscriptions.noActiveSubscriptionsDesc') }}
        </p>
      </div>

      <!-- Subscriptions Grid -->
      <div v-else-if="subscriptions.length > 0" class="grid gap-6 lg:grid-cols-2">
        <div
          v-for="subscription in subscriptions"
          :key="subscription.id"
          class="overflow-hidden rounded-2xl border bg-white shadow-sm transition-shadow hover:shadow-lg dark:bg-dark-800 dark:shadow-none"
          :class="platformBorderClass(subscription.group?.platform || '')"
        >
          <!-- Header -->
          <div
            class="relative flex items-center justify-between gap-3 overflow-hidden p-4"
            :class="isOpenAiPlatform(subscription) ? '' : ['bg-gradient-to-br', platformGradientClass(subscription.group?.platform || '')]"
            :style="isOpenAiPlatform(subscription) ? openAiHeaderStyle : undefined"
          >
            <div class="relative flex min-w-0 items-center gap-3">
              <div :class="['h-2 w-2 shrink-0 rounded-full bg-white shadow-[0_0_6px_rgba(255,255,255,0.8)]']" />
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <h3 class="truncate font-semibold text-white drop-shadow-sm">
                    {{ subscription.group?.name || `Group #${subscription.group_id}` }}
                  </h3>
                  <span class="shrink-0 rounded-md border border-white/30 bg-white/10 px-2 py-0.5 text-[11px] font-medium text-white backdrop-blur-sm">
                    {{ platformLabel(subscription.group?.platform || '') }}
                  </span>
                </div>
                <p v-if="subscription.group?.description" class="mt-0.5 truncate text-xs text-white/70">
                  {{ subscription.group.description }}
                </p>
                <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-white/60">
                  <span v-if="showRateMultiplier">{{ t('payment.planCard.rate') }}: ×{{ subscription.group?.rate_multiplier ?? 1 }}</span>
                  <span v-if="subscriptionHasPeakRate(subscription)" class="text-amber-300">
                    {{ t('payment.planCard.peakRate') }}: {{ subscriptionPeakRateLabel(subscription) }}
                  </span>
                </div>
              </div>
            </div>
            <div class="relative flex shrink-0 flex-col items-end gap-2">
              <span
                :class="[
                  'rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset backdrop-blur-sm',
                  subscription.status === 'active'
                    ? 'bg-emerald-400/20 text-emerald-100 ring-emerald-300/30'
                    : subscription.status === 'expired'
                      ? 'bg-white/10 text-white/70 ring-white/20'
                      : 'bg-red-400/20 text-red-100 ring-red-300/30'
                ]"
              >
                {{ t(`userSubscriptions.status.${subscription.status}`) }}
              </span>
              <button
                v-if="subscription.status === 'active' && paymentEnabled"
                class="rounded-lg bg-white px-3 py-1.5 text-xs font-semibold text-gray-900 shadow-sm transition-colors hover:bg-white/90 active:bg-white/80"
                @click="router.push({ path: '/purchase', query: { tab: 'subscription', group: String(subscription.group_id) } })"
              >
                {{ t('payment.renewNow') }}
              </button>
            </div>
          </div>

          <!-- Usage Progress -->
          <div class="space-y-4 p-4">
            <!-- Expiration Info -->
            <div v-if="subscription.expires_at" class="flex items-center justify-between text-sm">
              <span class="text-gray-500 dark:text-dark-400">{{
                t('userSubscriptions.expires')
              }}</span>
              <span :class="getExpirationClass(subscription.expires_at)">
                {{ formatExpirationDate(subscription.expires_at) }}
              </span>
            </div>
            <div v-else class="flex items-center justify-between text-sm">
              <span class="text-gray-500 dark:text-dark-400">{{
                t('userSubscriptions.expires')
              }}</span>
              <span class="text-gray-700 dark:text-gray-300">{{
                t('userSubscriptions.noExpiration')
              }}</span>
            </div>

            <!-- Daily Usage -->
            <div v-if="subscription.group?.daily_limit_usd" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('userSubscriptions.daily') }}
                </span>
                <span class="text-sm text-gray-500 dark:text-dark-400">
                  ${{ (subscription.daily_usage_usd || 0).toFixed(2) }} / ${{
                    subscription.group.daily_limit_usd.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                <div
                  class="absolute inset-y-0 left-0 rounded-full transition-all duration-300"
                  :class="
                    getProgressBarClass(
                      subscription.daily_usage_usd,
                      subscription.group.daily_limit_usd
                    )
                  "
                  :style="{
                    width: getProgressWidth(
                      subscription.daily_usage_usd,
                      subscription.group.daily_limit_usd
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="subscription.daily_window_start"
                class="text-xs text-gray-500 dark:text-dark-400"
              >
                {{ formatDailyUsageWindow(subscription) }}
              </p>
            </div>

            <!-- Weekly Usage -->
            <div v-if="subscription.group?.weekly_limit_usd" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('userSubscriptions.weekly') }}
                </span>
                <span class="text-sm text-gray-500 dark:text-dark-400">
                  ${{ (subscription.weekly_usage_usd || 0).toFixed(2) }} / ${{
                    subscription.group.weekly_limit_usd.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                <div
                  class="absolute inset-y-0 left-0 rounded-full transition-all duration-300"
                  :class="
                    getProgressBarClass(
                      subscription.weekly_usage_usd,
                      subscription.group.weekly_limit_usd
                    )
                  "
                  :style="{
                    width: getProgressWidth(
                      subscription.weekly_usage_usd,
                      subscription.group.weekly_limit_usd
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="subscription.weekly_window_start"
                class="text-xs text-gray-500 dark:text-dark-400"
              >
                {{
                  t('userSubscriptions.resetIn', {
                    time: formatResetTime(subscription.weekly_window_start, 168)
                  })
                }}
              </p>
            </div>

            <!-- Monthly Usage -->
            <div v-if="subscription.group?.monthly_limit_usd" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('userSubscriptions.monthly') }}
                </span>
                <span class="text-sm text-gray-500 dark:text-dark-400">
                  ${{ (subscription.monthly_usage_usd || 0).toFixed(2) }} / ${{
                    subscription.group.monthly_limit_usd.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                <div
                  class="absolute inset-y-0 left-0 rounded-full transition-all duration-300"
                  :class="
                    getProgressBarClass(
                      subscription.monthly_usage_usd,
                      subscription.group.monthly_limit_usd
                    )
                  "
                  :style="{
                    width: getProgressWidth(
                      subscription.monthly_usage_usd,
                      subscription.group.monthly_limit_usd
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="subscription.monthly_window_start"
                class="text-xs text-gray-500 dark:text-dark-400"
              >
                {{
                  t('userSubscriptions.resetIn', {
                    time: formatResetTime(subscription.monthly_window_start, 720)
                  })
                }}
              </p>
            </div>

            <!-- No limits configured - Unlimited badge -->
            <div
              v-if="
                !subscription.group?.daily_limit_usd &&
                !subscription.group?.weekly_limit_usd &&
                !subscription.group?.monthly_limit_usd
              "
              class="flex items-center justify-center rounded-xl bg-gradient-to-r from-emerald-50 to-teal-50 py-6 dark:from-emerald-900/20 dark:to-teal-900/20"
            >
              <div class="flex items-center gap-3">
                <span class="text-4xl text-emerald-600 dark:text-emerald-400">∞</span>
                <div>
                  <p class="text-sm font-medium text-emerald-700 dark:text-emerald-300">
                    {{ t('userSubscriptions.unlimited') }}
                  </p>
                  <p class="text-xs text-emerald-600/70 dark:text-emerald-400/70">
                    {{ t('userSubscriptions.unlimitedDesc') }}
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { useShowRateMultiplier } from '@/composables/useRateMultiplierVisibility'
import subscriptionsAPI from '@/api/subscriptions'
import type { UserSubscription } from '@/types'
import bundleSubscriptionsAPI, { type BundleSubscription } from '@/api/bundleSubscriptions'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTimeToMinute } from '@/utils/format'
import { hasPeakRate, formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { platformBorderClass, platformGradientClass, platformLabel } from '@/utils/platformColors'
import {
  getExpirationDateRelation,
  getRemainingDurationParts,
  isOneTimeDailyQuota,
  type RemainingDurationParts
} from '@/utils/subscriptionQuota'
import codexProBg from '@/assets/images/subscriptions/codex-pro-bg.jpg'

function isOpenAiPlatform(subscription: UserSubscription): boolean {
  return subscription.group?.platform === 'openai'
}

// `contain` (not `cover`) so the full banner — including the top logo row and
// the bottom tagline — always shows regardless of the header's content-driven
// height; `cover` was cropping both on short cards.
const openAiHeaderStyle = {
  backgroundImage: `linear-gradient(115deg, rgba(6,6,24,0.7) 0%, rgba(6,6,30,0.4) 55%, rgba(6,6,26,0.7) 100%), url(${codexProBg})`,
  backgroundRepeat: 'no-repeat',
  backgroundSize: 'cover, contain',
  backgroundPosition: 'center, right center',
  backgroundColor: '#05050f'
}

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()
const showRateMultiplier = useShowRateMultiplier()

const paymentEnabled = computed(() => appStore.cachedPublicSettings?.payment_enabled !== false)

const subscriptions = ref<UserSubscription[]>([])
const bundleSubscriptions = ref<BundleSubscription[]>([])
const loading = ref(true)
const bundleEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.bundleSubscriptions))

function subscriptionHasPeakRate(subscription: UserSubscription): boolean {
  return hasPeakRate(subscription.group)
}

function subscriptionPeakRateLabel(subscription: UserSubscription): string {
  return formatPeakRateWindow(subscription.group, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset))
}

async function loadSubscriptions() {
  try {
    loading.value = true
    subscriptions.value = await subscriptionsAPI.getMySubscriptions()
    if (bundleEnabled.value) {
      const response = await bundleSubscriptionsAPI.getMine()
      bundleSubscriptions.value = response.data
    }
  } catch (error) {
    console.error('Failed to load subscriptions:', error)
    appStore.showError(t('userSubscriptions.failedToLoad'))
  } finally {
    loading.value = false
  }
}

async function cancelBundle(id: number) {
  try {
    await bundleSubscriptionsAPI.cancelPending(id)
    const current = bundleSubscriptions.value.find((subscription) => subscription.id === id)
    if (current) current.status = 'revoked'
  } catch (error) {
    console.error('Failed to cancel bundle subscription:', error)
    appStore.showError(t('common.error'))
  }
}

function getProgressWidth(used: number | undefined, limit: number | null | undefined): string {
  if (!limit || limit === 0) return '0%'
  const percentage = Math.min(((used || 0) / limit) * 100, 100)
  return `${percentage}%`
}

function getProgressBarClass(used: number | undefined, limit: number | null | undefined): string {
  if (!limit || limit === 0) return 'bg-gray-400'
  const percentage = ((used || 0) / limit) * 100
  if (percentage >= 90) return 'bg-red-500'
  if (percentage >= 70) return 'bg-orange-500'
  return 'bg-green-500'
}

function formatExpirationDate(expiresAt: string): string {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))
  const relation = getExpirationDateRelation(expires, now)

  if (relation === null) return ''

  if (relation === 'expired') {
    return t('userSubscriptions.status.expired')
  }

  const dateStr = formatDateTimeToMinute(expires)

  if (relation === 'today') {
    return `${dateStr} (${t('common.today')})`
  }
  if (relation === 'tomorrow') {
    return `${dateStr} (${t('common.tomorrow')})`
  }

  return t('userSubscriptions.daysRemaining', { days }) + ` (${dateStr})`
}

function getExpirationClass(expiresAt: string): string {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))

  if (diff <= 0) return 'text-red-600 dark:text-red-400 font-medium'
  if (days <= 3) return 'text-red-600 dark:text-red-400'
  if (days <= 7) return 'text-orange-600 dark:text-orange-400'
  return 'text-gray-700 dark:text-gray-300'
}

function formatDurationParts(parts: RemainingDurationParts): string {
  if (parts.days > 0) {
    return `${parts.days}d ${parts.hours}h`
  }

  if (parts.hours > 0) {
    return `${parts.hours}h ${parts.minutes}m`
  }

  return `${parts.minutes}m`
}

function formatDailyUsageWindow(subscription: UserSubscription): string {
  if (isOneTimeDailyQuota(subscription) && subscription.expires_at) {
    const parts = getRemainingDurationParts(subscription.expires_at)
    if (!parts) return t('userSubscriptions.windowNotActive')
    return t('userSubscriptions.quotaEndsIn', { time: formatDurationParts(parts) })
  }

  return t('userSubscriptions.resetIn', {
    time: formatResetTime(subscription.daily_window_start, 24)
  })
}

function formatResetTime(windowStart: string | null, windowHours: number): string {
  if (!windowStart) return t('userSubscriptions.windowNotActive')

  const start = new Date(windowStart)
  const end = new Date(start.getTime() + windowHours * 60 * 60 * 1000)
  const parts = getRemainingDurationParts(end)

  return parts ? formatDurationParts(parts) : t('userSubscriptions.windowNotActive')
}

onMounted(() => {
  loadSubscriptions()
})
</script>
