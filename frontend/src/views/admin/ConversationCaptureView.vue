<template>
  <div class="space-y-6">
    <!-- Filters -->
    <div class="card p-6">
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 class="flex items-center gap-2 text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.conversationCapture.search.title') }}
            <span class="rounded bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-900/40 dark:text-amber-300">
              Beta
            </span>
          </h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.conversationCapture.search.description') }}
          </p>
        </div>
      </div>

      <div class="grid grid-cols-1 gap-3 md:grid-cols-2 lg:grid-cols-4">
        <div>
          <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.conversationCapture.search.account') }} <span class="text-red-500">*</span>
          </label>
          <select v-model.number="filters.accountId" class="input w-full">
            <option :value="0">{{ t('admin.conversationCapture.search.accountPlaceholder') }}</option>
            <option v-for="account in accounts" :key="account.id" :value="account.id">
              {{ account.name }} · {{ account.platform }}
            </option>
          </select>
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.conversationCapture.search.start') }} <span class="text-red-500">*</span>
          </label>
          <input v-model="filters.start" type="datetime-local" class="input w-full" />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.conversationCapture.search.end') }} <span class="text-red-500">*</span>
          </label>
          <input v-model="filters.end" type="datetime-local" class="input w-full" />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.conversationCapture.search.keyword') }}
          </label>
          <input
            v-model="filters.keyword"
            class="input w-full"
            :placeholder="t('admin.conversationCapture.search.keywordPlaceholder')"
            @keyup.enter="search"
          />
        </div>
      </div>

      <div class="mt-3 flex flex-wrap items-center gap-2">
        <button type="button" class="btn btn-secondary btn-sm" @click="applyPreset(24)">
          {{ t('admin.conversationCapture.search.last24h') }}
        </button>
        <button type="button" class="btn btn-secondary btn-sm" @click="applyPreset(24 * 7)">
          {{ t('admin.conversationCapture.search.last7d') }}
        </button>
        <button type="button" class="btn btn-primary btn-sm" :disabled="searching" @click="search">
          {{ searching ? t('common.loading') : t('common.search') }}
        </button>
        <p class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.conversationCapture.search.limitsHint') }}
        </p>
      </div>
    </div>

    <!-- Summary -->
    <div v-if="summary" class="card grid grid-cols-2 gap-4 p-6 md:grid-cols-4">
      <div>
        <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.conversationCapture.summary.total') }}</div>
        <div class="text-lg font-semibold text-gray-900 dark:text-white">{{ summary.total }}</div>
      </div>
      <div>
        <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.conversationCapture.summary.users') }}</div>
        <div class="text-lg font-semibold text-gray-900 dark:text-white">{{ summary.user_count }}</div>
      </div>
      <div>
        <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.conversationCapture.summary.input') }}</div>
        <div class="text-lg font-semibold text-gray-900 dark:text-white">{{ formatBytes(summary.input_bytes) }}</div>
      </div>
      <div>
        <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.conversationCapture.summary.output') }}</div>
        <div class="text-lg font-semibold text-gray-900 dark:text-white">{{ formatBytes(summary.output_bytes) }}</div>
      </div>
    </div>

    <!-- Results -->
    <div class="card p-0">
      <div v-if="!records.length" class="p-6 text-center text-sm text-gray-500 dark:text-gray-400">
        {{ searched ? t('admin.conversationCapture.empty') : t('admin.conversationCapture.prompt') }}
      </div>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="border-b border-gray-200 text-left text-xs uppercase text-gray-500 dark:border-gray-700 dark:text-gray-400">
            <tr>
              <th class="px-4 py-2">{{ t('admin.conversationCapture.table.time') }}</th>
              <th class="px-4 py-2">{{ t('admin.conversationCapture.table.user') }}</th>
              <th class="px-4 py-2">{{ t('admin.conversationCapture.table.model') }}</th>
              <th class="px-4 py-2">{{ t('admin.conversationCapture.table.preview') }}</th>
              <th class="px-4 py-2 text-right">{{ t('admin.conversationCapture.table.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-gray-800">
            <tr
              v-for="record in records"
              :key="record.id"
              class="align-top transition-colors"
              :class="
                isViewed(record)
                  ? 'bg-gray-100/70 text-gray-400 dark:bg-gray-800/70 dark:text-gray-500'
                  : 'hover:bg-gray-50 dark:hover:bg-gray-800/40'
              "
            >
              <td class="whitespace-nowrap px-4 py-2 text-xs text-gray-600 dark:text-gray-400">
                {{ formatTime(record.created_at) }}
                <span
                  v-if="isViewed(record)"
                  class="mt-1 block w-fit rounded bg-gray-200 px-1.5 py-0.5 text-[10px] font-medium text-gray-600 dark:bg-gray-700 dark:text-gray-300"
                >
                  {{ t('admin.conversationCapture.table.viewed') }}
                </span>
              </td>
              <td class="whitespace-nowrap px-4 py-2">
                <div class="text-gray-900 dark:text-gray-100">{{ record.user_email || '-' }}</div>
                <div class="text-xs text-gray-500 dark:text-gray-400">{{ record.api_key_name || '-' }}</div>
              </td>
              <td class="whitespace-nowrap px-4 py-2 text-xs text-gray-600 dark:text-gray-400">
                {{ record.model || '-' }}
              </td>
              <td class="max-w-xl px-4 py-2">
                <p class="whitespace-pre-wrap break-words text-gray-800 dark:text-gray-200">
                  {{ record.input_preview || t('admin.conversationCapture.table.noPreview') }}
                </p>
              </td>
              <td class="whitespace-nowrap px-4 py-2 text-right">
                <button
                  type="button"
                  class="btn btn-secondary btn-xs"
                  :disabled="!record.object_key"
                  :title="record.object_key ? '' : t('admin.conversationCapture.table.noFullText')"
                  @click="openFull(record)"
                >
                  {{ t('admin.conversationCapture.table.viewFull') }}
                </button>
                <button
                  v-if="record.user_id"
                  type="button"
                  class="btn btn-danger btn-xs ml-2"
                  :disabled="banningUserId === record.user_id"
                  @click="banUser(record)"
                >
                  {{ t('admin.conversationCapture.table.banUser') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Full record drawer -->
    <div
      v-if="fullRecordOpen"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      @click.self="fullRecordOpen = false"
    >
      <div class="flex max-h-[85vh] w-full max-w-4xl flex-col rounded-lg bg-white shadow-xl dark:bg-gray-900">
        <div class="flex items-start justify-between gap-4 border-b border-gray-200 px-4 py-3 dark:border-gray-700">
          <div class="min-w-0">
            <h4 class="font-semibold text-gray-900 dark:text-white">
              {{ t('admin.conversationCapture.full.title') }}
            </h4>
            <!-- 正文很长，滚动几屏后很容易忘了这个弹窗是从哪一行点开的。
                 身份信息常驻标题栏，随时能对上是谁。 -->
            <dl v-if="fullRecordTarget" class="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs">
              <div class="flex gap-1">
                <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.conversationCapture.full.user') }}</dt>
                <dd class="font-medium text-gray-900 dark:text-gray-100">
                  {{ fullRecordTarget.user_email || '-' }}
                  <span class="font-normal text-gray-500 dark:text-gray-400">#{{ fullRecordTarget.user_id ?? '-' }}</span>
                </dd>
              </div>
              <div class="flex gap-1">
                <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.conversationCapture.full.group') }}</dt>
                <dd class="font-medium text-gray-900 dark:text-gray-100">{{ fullRecordTarget.group_name || '-' }}</dd>
              </div>
              <div class="flex gap-1">
                <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.conversationCapture.full.platform') }}</dt>
                <dd class="font-medium text-gray-900 dark:text-gray-100">{{ fullRecordTarget.platform || '-' }}</dd>
              </div>
              <div class="flex min-w-0 gap-1">
                <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.conversationCapture.full.requestId') }}</dt>
                <dd class="truncate font-mono text-gray-700 dark:text-gray-300">{{ fullRecordTarget.request_id }}</dd>
              </div>
            </dl>
          </div>
          <button type="button" class="btn btn-secondary btn-xs shrink-0" @click="fullRecordOpen = false">
            {{ t('common.close') }}
          </button>
        </div>
        <div class="flex-1 overflow-auto p-4">
          <p v-if="fullRecordLoading" class="text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</p>
          <pre v-else class="whitespace-pre-wrap break-words text-xs text-gray-800 dark:text-gray-200">{{ fullRecordText }}</pre>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import { useAppStore } from '@/stores'
import type {
  ConversationCaptureRecord,
  ConversationCaptureSummary,
} from '@/api/admin/conversationCapture'

const { t } = useI18n()
const appStore = useAppStore()

interface AccountOption {
  id: number
  name: string
  platform: string
}

const accounts = ref<AccountOption[]>([])
const records = ref<ConversationCaptureRecord[]>([])
const summary = ref<ConversationCaptureSummary | null>(null)
const searching = ref(false)
const searched = ref(false)
const banningUserId = ref<number | null>(null)

const fullRecordOpen = ref(false)
const fullRecordLoading = ref(false)
const fullRecordText = ref('')
const fullRecordTarget = ref<ConversationCaptureRecord | null>(null)

// 已查看标记跨刷新保留：审计时经常翻到一半刷新页面，丢了标记就得从头重看。
// 上限防止 localStorage 无限增长。
const VIEWED_STORAGE_KEY = 'convlog:viewed-request-ids'
const VIEWED_LIMIT = 500
const viewedRequestIds = ref<string[]>(loadViewedIds())

function loadViewedIds(): string[] {
  try {
    const raw = window.localStorage.getItem(VIEWED_STORAGE_KEY)
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === 'string') : []
  } catch {
    return []
  }
}

function isViewed(record: ConversationCaptureRecord): boolean {
  return viewedRequestIds.value.includes(record.request_id)
}

function markViewed(requestId: string): void {
  if (!requestId || viewedRequestIds.value.includes(requestId)) return
  const next = [...viewedRequestIds.value, requestId].slice(-VIEWED_LIMIT)
  viewedRequestIds.value = next
  try {
    window.localStorage.setItem(VIEWED_STORAGE_KEY, JSON.stringify(next))
  } catch {
    // 存储被禁用或写满时只丢标记，不影响检索。
  }
}

const filters = reactive({
  accountId: 0,
  start: '',
  end: '',
  keyword: '',
})

// datetime-local 用的是本地时间字符串，没有时区后缀，需要手工转换两次。
function toLocalInput(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function applyPreset(hours: number): void {
  const end = new Date()
  const start = new Date(end.getTime() - hours * 3600 * 1000)
  filters.start = toLocalInput(start)
  filters.end = toLocalInput(end)
}

function formatTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

async function loadAccounts(): Promise<void> {
  try {
    // 账号池通常是几十到几百个，一次性拉全量做选择器即可。
    const response = await adminAPI.accounts.list(1, 500, { lite: 'true' })
    accounts.value = (response.items ?? []).map((account) => ({
      id: account.id,
      name: account.name,
      platform: account.platform,
    }))
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

async function search(): Promise<void> {
  if (!filters.accountId) {
    appStore.showError(t('admin.conversationCapture.search.accountRequired'))
    return
  }
  if (!filters.start || !filters.end) {
    appStore.showError(t('admin.conversationCapture.search.rangeRequired'))
    return
  }

  searching.value = true
  try {
    const result = await adminAPI.conversationCapture.searchCaptureRecords({
      account_id: filters.accountId,
      start: new Date(filters.start).toISOString(),
      end: new Date(filters.end).toISOString(),
      keyword: filters.keyword.trim() || undefined,
    })
    records.value = result.records ?? []
    summary.value = result.summary ?? null
    searched.value = true
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    searching.value = false
  }
}

async function openFull(record: ConversationCaptureRecord): Promise<void> {
  fullRecordOpen.value = true
  fullRecordLoading.value = true
  fullRecordText.value = ''
  fullRecordTarget.value = record
  // 点开即标记：读到一半关掉也算看过，重点是知道扫到哪一行了。
  markViewed(record.request_id)
  try {
    const result = await adminAPI.conversationCapture.getCaptureRecordFull(record.request_id)
    fullRecordText.value = JSON.stringify(result.record, null, 2)
  } catch (error) {
    fullRecordText.value = ''
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    fullRecordLoading.value = false
  }
}

async function banUser(record: ConversationCaptureRecord): Promise<void> {
  if (!record.user_id) return
  const confirmed = window.confirm(
    t('admin.conversationCapture.table.banConfirm', { email: record.user_email || record.user_id }),
  )
  if (!confirmed) return

  banningUserId.value = record.user_id
  try {
    await adminAPI.users.toggleStatus(record.user_id, 'disabled')
    appStore.showSuccess(t('admin.conversationCapture.table.banned'))
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    banningUserId.value = null
  }
}

onMounted(async () => {
  applyPreset(24)
  await loadAccounts()
})
</script>
