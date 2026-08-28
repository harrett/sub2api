<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Leaderboard -->
      <div class="card p-4">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('admin.affiliates.stats.leaderboardTitle') }}
          </h3>
          <div class="flex flex-wrap items-center gap-3">
            <input
              v-model="leaderboardFilters.start_at"
              type="date"
              class="input w-full sm:w-40"
              :title="t('admin.affiliates.records.startAt')"
              @change="reloadLeaderboard"
            />
            <input
              v-model="leaderboardFilters.end_at"
              type="date"
              class="input w-full sm:w-40"
              :title="t('admin.affiliates.records.endAt')"
              @change="reloadLeaderboard"
            />
            <button
              class="btn btn-secondary px-2 md:px-3"
              :disabled="leaderboardLoading"
              :title="t('common.refresh')"
              @click="loadLeaderboard"
            >
              <Icon name="refresh" size="md" :class="leaderboardLoading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>

        <DataTable
          :columns="leaderboardColumns"
          :data="leaderboardEntries"
          :loading="leaderboardLoading"
          :server-side-sort="true"
          default-sort-key="invite_count"
          default-sort-order="desc"
          sort-storage-key="admin-affiliate-leaderboard-sort"
          @sort="handleLeaderboardSort"
        >
          <template #cell-rank="{ row }">
            <span class="font-mono text-sm text-gray-500 dark:text-dark-400">#{{ row.rank }}</span>
          </template>
          <template #cell-inviter="{ row }">
            <div class="space-y-0.5">
              <div class="font-mono text-sm text-gray-900 dark:text-white">#{{ row.inviter_id }}</div>
              <div class="max-w-56 truncate text-sm font-medium text-gray-700 dark:text-gray-300">{{ row.inviter_email || '-' }}</div>
              <div class="max-w-56 truncate text-sm text-gray-500 dark:text-dark-400">{{ row.inviter_username || '-' }}</div>
            </div>
          </template>
          <template #cell-aff_code="{ row }">
            <span class="font-mono text-sm text-gray-700 dark:text-gray-300">{{ row.aff_code || '-' }}</span>
          </template>
          <template #cell-invite_count="{ row }">
            <span class="text-sm font-semibold text-gray-900 dark:text-white">{{ row.invite_count }}</span>
          </template>
          <template #cell-total_rebate="{ row }">
            <span class="text-sm font-semibold text-emerald-600 dark:text-emerald-400">${{ formatAmount(row.total_rebate) }}</span>
          </template>
          <template #cell-last_invited_at="{ row }">
            <span class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(row.last_invited_at) }}</span>
          </template>
        </DataTable>

        <div class="mt-4">
          <Pagination
            v-if="leaderboardPagination.total > 0"
            :page="leaderboardPagination.page"
            :total="leaderboardPagination.total"
            :page-size="leaderboardPagination.page_size"
            @update:page="handleLeaderboardPageChange"
            @update:pageSize="handleLeaderboardPageSizeChange"
          />
        </div>
      </div>

      <!-- Invite time distribution -->
      <div class="card p-4">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('admin.affiliates.stats.timelineTitle') }}
          </h3>
          <div class="flex flex-wrap items-center gap-3">
            <div class="relative w-full sm:w-64">
              <div
                v-if="selectedUser"
                class="flex items-center justify-between rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 dark:border-dark-700 dark:bg-dark-800"
              >
                <div class="min-w-0 truncate text-sm">
                  <span class="font-medium text-gray-900 dark:text-white">{{ selectedUser.email }}</span>
                  <span class="ml-1 text-xs text-gray-500">(#{{ selectedUser.id }})</span>
                </div>
                <button
                  type="button"
                  class="ml-2 shrink-0 text-lg leading-none text-gray-400 hover:text-red-600"
                  :title="t('admin.affiliates.stats.changeUser')"
                  @click="clearSelectedUser"
                >
                  ×
                </button>
              </div>
              <template v-else>
                <input
                  v-model="userQuery"
                  type="text"
                  class="input"
                  :placeholder="t('admin.affiliates.stats.userPlaceholder')"
                  @input="onUserSearchInput"
                />
                <div
                  v-if="userResults.length > 0"
                  class="absolute z-10 mt-1 max-h-40 w-full overflow-y-auto rounded border border-gray-200 bg-white shadow-lg dark:border-dark-700 dark:bg-dark-800"
                >
                  <button
                    v-for="u in userResults"
                    :key="u.id"
                    type="button"
                    class="w-full px-3 py-1.5 text-left text-sm hover:bg-gray-100 dark:hover:bg-dark-700"
                    @click="selectUser(u)"
                  >
                    {{ u.email }} <span class="text-xs text-gray-500">({{ u.username }})</span>
                  </button>
                </div>
              </template>
            </div>
            <select v-model="timelineFilters.granularity" class="input w-full sm:w-28" @change="loadTimeline">
              <option value="day">{{ t('admin.affiliates.stats.granularityDay') }}</option>
              <option value="week">{{ t('admin.affiliates.stats.granularityWeek') }}</option>
              <option value="month">{{ t('admin.affiliates.stats.granularityMonth') }}</option>
            </select>
            <input v-model="timelineFilters.start_at" type="date" class="input w-full sm:w-40" @change="loadTimeline" />
            <input v-model="timelineFilters.end_at" type="date" class="input w-full sm:w-40" @change="loadTimeline" />
            <button
              class="btn btn-secondary px-2 md:px-3"
              :disabled="timelineLoading"
              :title="t('common.refresh')"
              @click="loadTimeline"
            >
              <Icon name="refresh" size="md" :class="timelineLoading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>

        <div class="h-64">
          <div v-if="timelineLoading" class="flex h-full items-center justify-center">
            <LoadingSpinner size="md" />
          </div>
          <Bar v-else-if="timelineChartData" :data="timelineChartData" :options="timelineChartOptions" />
          <div v-else class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.affiliates.stats.noData') }}
          </div>
        </div>
      </div>

      <!-- Top-half inviters -->
      <div class="card p-4">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('admin.affiliates.stats.topHalfTitle') }}
          </h3>
          <div class="flex flex-wrap items-center gap-3">
            <div class="inline-flex rounded-lg border border-gray-200 p-0.5 dark:border-dark-700">
              <button
                type="button"
                class="rounded-md px-3 py-1 text-sm transition-colors"
                :class="topHalfFilters.mode === 'headcount' ? 'bg-primary-500 text-white' : 'text-gray-600 dark:text-gray-300'"
                @click="setTopHalfMode('headcount')"
              >
                {{ t('admin.affiliates.stats.modeHeadcount') }}
              </button>
              <button
                type="button"
                class="rounded-md px-3 py-1 text-sm transition-colors"
                :class="topHalfFilters.mode === 'volume' ? 'bg-primary-500 text-white' : 'text-gray-600 dark:text-gray-300'"
                @click="setTopHalfMode('volume')"
              >
                {{ t('admin.affiliates.stats.modeVolume') }}
              </button>
            </div>
            <input v-model="topHalfFilters.start_at" type="date" class="input w-full sm:w-40" @change="loadTopHalf" />
            <input v-model="topHalfFilters.end_at" type="date" class="input w-full sm:w-40" @change="loadTopHalf" />
            <button
              class="btn btn-secondary px-2 md:px-3"
              :disabled="topHalfLoading"
              :title="t('common.refresh')"
              @click="loadTopHalf"
            >
              <Icon name="refresh" size="md" :class="topHalfLoading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>
        <p class="mb-4 text-sm text-gray-500 dark:text-dark-400">
          {{ topHalfFilters.mode === 'volume' ? t('admin.affiliates.stats.modeVolumeHint') : t('admin.affiliates.stats.modeHeadcountHint') }}
        </p>

        <div v-if="topHalfLoading" class="flex h-32 items-center justify-center">
          <LoadingSpinner size="md" />
        </div>
        <template v-else-if="topHalfSummary">
          <div class="mb-6 grid gap-3 sm:grid-cols-4">
            <TopHalfStat :label="t('admin.affiliates.stats.totalInviterCount')" :value="String(topHalfSummary.total_inviter_count)" />
            <TopHalfStat :label="topHalfCountLabel" :value="String(topHalfSummary.top_half_count)" />
            <TopHalfStat :label="t('admin.affiliates.stats.totalInviteCount')" :value="String(topHalfSummary.total_invite_count)" />
            <TopHalfStat :label="t('admin.affiliates.stats.topHalfInvitePercent')" :value="formatPercent(topHalfSummary.top_half_invite_percent)" />
          </div>

          <div class="grid gap-6 lg:grid-cols-[220px_1fr]">
            <div class="mx-auto h-48 w-48">
              <Doughnut v-if="topHalfChartData" :data="topHalfChartData" :options="topHalfChartOptions" />
            </div>

            <DataTable :columns="topHalfColumns" :data="topHalfSummary.items" :loading="false">
              <template #cell-rank="{ row }">
                <span class="font-mono text-sm text-gray-500 dark:text-dark-400">#{{ row.rank }}</span>
              </template>
              <template #cell-inviter="{ row }">
                <div class="space-y-0.5">
                  <div class="font-mono text-sm text-gray-900 dark:text-white">#{{ row.inviter_id }}</div>
                  <div class="max-w-56 truncate text-sm font-medium text-gray-700 dark:text-gray-300">{{ row.inviter_email || '-' }}</div>
                  <div class="max-w-56 truncate text-sm text-gray-500 dark:text-dark-400">{{ row.inviter_username || '-' }}</div>
                </div>
              </template>
              <template #cell-aff_code="{ row }">
                <span class="font-mono text-sm text-gray-700 dark:text-gray-300">{{ row.aff_code || '-' }}</span>
              </template>
              <template #cell-invite_count="{ row }">
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{ row.invite_count }}</span>
              </template>
              <template #cell-total_rebate="{ row }">
                <span class="text-sm font-semibold text-emerald-600 dark:text-emerald-400">${{ formatAmount(row.total_rebate) }}</span>
              </template>
              <template #cell-last_invited_at="{ row }">
                <span class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(row.last_invited_at) }}</span>
              </template>
            </DataTable>
          </div>
        </template>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, ArcElement, Tooltip, Legend } from 'chart.js'
import { Bar, Doughnut } from 'vue-chartjs'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import type { Column } from '@/components/common/types'
import { useAppStore } from '@/stores/app'
import {
  affiliatesAPI,
  type AffiliateLeaderboardEntry,
  type AffiliateInviteTimelinePoint,
  type AffiliateTopHalfSummary,
  type AffiliateTopHalfMode,
  type SimpleUser,
} from '@/api/admin/affiliates'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatDateTime as formatDisplayDateTime } from '@/utils/format'

ChartJS.register(CategoryScale, LinearScale, BarElement, ArcElement, Tooltip, Legend)

const { t } = useI18n()
const appStore = useAppStore()

type LeaderboardRow = AffiliateLeaderboardEntry & { rank: number }

function userTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    return 'UTC'
  }
}

function formatAmount(value: number | null | undefined): string {
  return Number(value || 0).toFixed(2)
}

function formatDateTime(value: string | null | undefined): string {
  return value ? formatDisplayDateTime(value) : '-'
}

function formatPercent(value: number | null | undefined): string {
  const rounded = Math.round(Number(value || 0) * 100) / 100
  return `${rounded}%`
}

// ---- Leaderboard ----
const leaderboardLoading = ref(false)
const leaderboardEntries = ref<LeaderboardRow[]>([])
const leaderboardFilters = reactive({ start_at: '', end_at: '' })
const leaderboardPagination = reactive({ page: 1, page_size: 20, total: 0 })
const leaderboardSortBy = ref<'invite_count' | 'rebate_amount'>('invite_count')

const leaderboardColumns = computed<Column[]>(() => [
  { key: 'rank', label: '#' },
  { key: 'inviter', label: t('admin.affiliates.records.inviter') },
  { key: 'aff_code', label: t('admin.affiliates.records.affCode') },
  { key: 'invite_count', label: t('admin.affiliates.stats.inviteCount'), sortable: true },
  { key: 'total_rebate', label: t('admin.affiliates.records.totalRebate'), sortable: true },
  { key: 'last_invited_at', label: t('admin.affiliates.stats.lastInvitedAt') },
])

async function loadLeaderboard() {
  leaderboardLoading.value = true
  try {
    const res = await affiliatesAPI.getLeaderboard({
      page: leaderboardPagination.page,
      page_size: leaderboardPagination.page_size,
      start_at: leaderboardFilters.start_at || undefined,
      end_at: leaderboardFilters.end_at || undefined,
      sort_by: leaderboardSortBy.value,
      timezone: userTimezone(),
    })
    const offset = (leaderboardPagination.page - 1) * leaderboardPagination.page_size
    leaderboardEntries.value = (res.items || []).map((item, index) => ({ ...item, rank: offset + index + 1 }))
    leaderboardPagination.total = res.total || 0
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.affiliates.errors', t('common.error')))
  } finally {
    leaderboardLoading.value = false
  }
}

function reloadLeaderboard() {
  leaderboardPagination.page = 1
  void loadLeaderboard()
}

function handleLeaderboardPageChange(page: number) {
  leaderboardPagination.page = page
  void loadLeaderboard()
}

function handleLeaderboardPageSizeChange(size: number) {
  leaderboardPagination.page_size = size
  leaderboardPagination.page = 1
  void loadLeaderboard()
}

function handleLeaderboardSort(key: string) {
  leaderboardSortBy.value = key === 'total_rebate' ? 'rebate_amount' : 'invite_count'
  leaderboardPagination.page = 1
  void loadLeaderboard()
}

// ---- Invite time distribution ----
const userQuery = ref('')
const userResults = ref<SimpleUser[]>([])
const selectedUser = ref<SimpleUser | null>(null)
let userSearchTimer: ReturnType<typeof setTimeout> | null = null

function onUserSearchInput() {
  const q = userQuery.value.trim()
  if (userSearchTimer) clearTimeout(userSearchTimer)
  if (!q) {
    userResults.value = []
    return
  }
  userSearchTimer = setTimeout(async () => {
    try {
      userResults.value = await affiliatesAPI.lookupUsers(q)
    } catch (error) {
      appStore.showError(extractI18nErrorMessage(error, t, 'admin.affiliates.errors', t('common.error')))
    }
  }, 300)
}

function selectUser(user: SimpleUser) {
  selectedUser.value = user
  userQuery.value = ''
  userResults.value = []
  void loadTimeline()
}

function clearSelectedUser() {
  selectedUser.value = null
  void loadTimeline()
}

const timelineLoading = ref(false)
const timelinePoints = ref<AffiliateInviteTimelinePoint[]>([])
const timelineFilters = reactive({ start_at: '', end_at: '', granularity: 'day' as 'day' | 'week' | 'month' })

async function loadTimeline() {
  timelineLoading.value = true
  try {
    timelinePoints.value = await affiliatesAPI.getInviteTimeline({
      inviter_id: selectedUser.value?.id,
      start_at: timelineFilters.start_at || undefined,
      end_at: timelineFilters.end_at || undefined,
      granularity: timelineFilters.granularity,
      timezone: userTimezone(),
    })
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.affiliates.errors', t('common.error')))
  } finally {
    timelineLoading.value = false
  }
}

function formatBucketLabel(value: string): string {
  if (!value) return '-'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  if (timelineFilters.granularity === 'month') return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

const timelineChartData = computed(() => {
  if (!timelinePoints.value.length) return null
  return {
    labels: timelinePoints.value.map((p) => formatBucketLabel(p.bucket)),
    datasets: [
      {
        label: t('admin.affiliates.stats.inviteCount'),
        data: timelinePoints.value.map((p) => p.invite_count),
        backgroundColor: 'rgba(59, 130, 246, 0.6)',
        borderColor: 'rgb(59, 130, 246)',
        borderWidth: 1,
      },
    ],
  }
})

const timelineChartOptions = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: false } },
  scales: {
    y: { beginAtZero: true, ticks: { precision: 0 } },
  },
}

// ---- Top-half inviters ----
const topHalfLoading = ref(false)
const topHalfSummary = ref<AffiliateTopHalfSummary | null>(null)
const topHalfFilters = reactive({ start_at: '', end_at: '', mode: 'headcount' as AffiliateTopHalfMode })

const topHalfColumns = computed<Column[]>(() => [
  { key: 'rank', label: '#' },
  { key: 'inviter', label: t('admin.affiliates.records.inviter') },
  { key: 'aff_code', label: t('admin.affiliates.records.affCode') },
  { key: 'invite_count', label: t('admin.affiliates.stats.inviteCount') },
  { key: 'total_rebate', label: t('admin.affiliates.records.totalRebate') },
  { key: 'last_invited_at', label: t('admin.affiliates.stats.lastInvitedAt') },
])

const topHalfCountLabel = computed(() =>
  topHalfFilters.mode === 'volume'
    ? t('admin.affiliates.stats.topHalfCountVolume')
    : t('admin.affiliates.stats.topHalfCount'),
)

function setTopHalfMode(mode: AffiliateTopHalfMode) {
  if (topHalfFilters.mode === mode) return
  topHalfFilters.mode = mode
  void loadTopHalf()
}

async function loadTopHalf() {
  topHalfLoading.value = true
  try {
    topHalfSummary.value = await affiliatesAPI.getTopHalfInviters({
      start_at: topHalfFilters.start_at || undefined,
      end_at: topHalfFilters.end_at || undefined,
      mode: topHalfFilters.mode,
      timezone: userTimezone(),
    })
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.affiliates.errors', t('common.error')))
  } finally {
    topHalfLoading.value = false
  }
}

const topHalfChartData = computed(() => {
  const summary = topHalfSummary.value
  if (!summary || summary.total_invite_count <= 0) return null
  const rest = summary.total_invite_count - summary.top_half_invite_count
  return {
    labels: [topHalfCountLabel.value, t('admin.affiliates.stats.bottomHalf')],
    datasets: [
      {
        data: [summary.top_half_invite_count, rest],
        backgroundColor: ['rgba(59, 130, 246, 0.8)', 'rgba(148, 163, 184, 0.4)'],
        borderWidth: 0,
      },
    ],
  }
})

const topHalfChartOptions = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { position: 'bottom' as const } },
}

const TopHalfStat = defineComponent({
  props: {
    label: { type: String, required: true },
    value: { type: String, required: true },
  },
  setup(statProps) {
    return () => h('div', { class: 'rounded-lg border border-gray-100 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-800' }, [
      h('div', { class: 'text-sm text-gray-500 dark:text-dark-400' }, statProps.label),
      h('div', { class: 'mt-1 text-lg font-semibold text-gray-900 dark:text-white' }, statProps.value),
    ])
  },
})

onMounted(() => {
  void loadLeaderboard()
  void loadTimeline()
  void loadTopHalf()
})
</script>
