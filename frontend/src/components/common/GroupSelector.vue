<template>
  <div>
    <label class="input-label">
      {{ t('admin.users.groups') }}
      <span class="font-normal text-gray-400">{{
        t('common.selectedCount', { count: selectedIds.length })
      }}</span>
      <span
        v-if="hiddenSelected.length > 0"
        class="ml-1 rounded bg-amber-100 px-1.5 py-0.5 text-xs font-normal text-amber-700 dark:bg-amber-900/40 dark:text-amber-300"
      >
        {{ t('common.groupSelector.hiddenBadge', { count: hiddenSelected.length }) }}
      </span>
    </label>
    <div
      v-if="isSearchable"
      class="flex items-center gap-2 rounded-t-lg border border-b-0 border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-600 dark:bg-dark-800"
    >
      <Icon name="search" size="sm" class="shrink-0 text-gray-400" />
      <input
        v-model="searchText"
        type="text"
        :placeholder="t('common.searchPlaceholder')"
        class="flex-1 bg-transparent text-sm text-gray-900 placeholder:text-gray-400 focus:outline-none dark:text-gray-100 dark:placeholder:text-dark-400"
      />
    </div>
    <div
      :class="[
        'grid max-h-32 grid-cols-2 gap-1 overflow-y-auto p-2',
        isSearchable
          ? 'rounded-b-lg border border-t-0 border-gray-200 bg-gray-50 dark:border-dark-600 dark:bg-dark-800'
          : 'rounded-lg border border-gray-200 bg-gray-50 dark:border-dark-600 dark:bg-dark-800'
      ]"
    >
      <label
        v-for="group in filteredGroups"
        :key="group.id"
        class="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 transition-colors hover:bg-white dark:hover:bg-dark-700"
        :title="t('admin.groups.rateAndAccounts', { rate: group.rate_multiplier, count: group.account_count || 0 })"
      >
        <input
          type="checkbox"
          :value="group.id"
          :checked="selectedIdSet.has(group.id)"
          @change="handleChange(group.id, ($event.target as HTMLInputElement).checked)"
          class="h-3.5 w-3.5 shrink-0 rounded border-gray-300 text-primary-500 focus:ring-primary-500 dark:border-dark-500"
        />
        <GroupBadge
          :name="group.name"
          :platform="group.platform"
          :subscription-type="group.subscription_type"
          :rate-multiplier="group.rate_multiplier"
          class="min-w-0 flex-1"
        />
        <span class="shrink-0 text-xs text-gray-400">{{ group.account_count || 0 }}</span>
      </label>
      <div
        v-if="filteredGroups.length === 0"
        class="col-span-2 py-2 text-center text-sm text-gray-500 dark:text-gray-400"
      >
        {{ t('common.noGroupsAvailable') }}
      </div>
    </div>

    <!--
      已选中但不在上方可选列表里的分组（平台不匹配 / 分组已停用 / 分组已删除）。
      不渲染它们会让"已选 N 个"和实际勾选数对不上，而且这些绑定会在保存时被静默写回。
    -->
    <div
      v-if="hiddenSelected.length > 0"
      class="mt-2 rounded-lg border border-amber-300 bg-amber-50 p-2 dark:border-amber-700/60 dark:bg-amber-900/20"
    >
      <p class="mb-1.5 text-xs text-amber-800 dark:text-amber-300">
        {{ t('common.groupSelector.hiddenHint') }}
      </p>
      <div class="grid max-h-32 grid-cols-1 gap-1 overflow-y-auto">
        <label
          v-for="item in hiddenSelected"
          :key="item.id"
          class="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 transition-colors hover:bg-white/70 dark:hover:bg-dark-700"
        >
          <input
            type="checkbox"
            checked
            :data-hidden-group-id="item.id"
            @change="handleChange(item.id, ($event.target as HTMLInputElement).checked)"
            class="h-3.5 w-3.5 shrink-0 rounded border-gray-300 text-primary-500 focus:ring-primary-500 dark:border-dark-500"
          />
          <GroupBadge
            v-if="item.group"
            :name="item.group.name"
            :platform="item.group.platform"
            :subscription-type="item.group.subscription_type"
            :rate-multiplier="item.group.rate_multiplier"
            class="min-w-0 flex-1"
          />
          <span v-else class="min-w-0 flex-1 truncate text-sm text-gray-600 dark:text-gray-300">
            {{ t('common.groupSelector.unknownGroupName', { id: item.id }) }}
          </span>
          <span class="shrink-0 text-xs text-amber-700 dark:text-amber-400">{{ item.reason }}</span>
        </label>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import GroupBadge from './GroupBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import type { AdminGroup, Group, GroupPlatform } from '@/types'

const { t } = useI18n()

interface Props {
  modelValue: number[]
  groups: AdminGroup[]
  platform?: GroupPlatform // Optional platform filter
  mixedScheduling?: boolean // For antigravity accounts: allow anthropic/gemini groups
  searchable?: boolean | 'auto'
  /**
   * 补充的分组元数据，用于解析已选中但不在 `groups` 里的分组（典型来源：
   * `/admin/groups/all` 默认不返回已停用分组，而账号上仍绑定着它们）。
   */
  fallbackGroups?: Group[]
}

const props = withDefaults(defineProps<Props>(), {
  searchable: 'auto'
})
const emit = defineEmits<{
  'update:modelValue': [value: number[]]
}>()

const searchText = ref('')

const isSearchable = computed(() => {
  if (props.searchable === 'auto') return props.groups.length > 5
  return props.searchable
})

// 去重后的选中项：modelValue 里出现重复 id 时不应把计数撑大
const selectedIds = computed(() => Array.from(new Set(props.modelValue)))
const selectedIdSet = computed(() => new Set(selectedIds.value))

// 平台过滤后的可选分组（不含搜索过滤，搜索只是临时视图）
const selectableGroups = computed(() => {
  if (!props.platform) return props.groups
  // antigravity 账户启用混合调度后，可选择 anthropic/gemini 分组
  if (props.platform === 'antigravity' && props.mixedScheduling) {
    return props.groups.filter(
      (g) => g.platform === 'antigravity' || g.platform === 'anthropic' || g.platform === 'gemini' || g.platform === 'composite'
    )
  }
  // 默认：只能选择同 platform 的分组；composite 分组可接收任意具体平台账号
  return props.groups.filter((g) => g.platform === props.platform || g.platform === 'composite')
})

const selectableIdSet = computed(() => new Set(selectableGroups.value.map((g) => g.id)))

const filteredGroups = computed(() => {
  if (!isSearchable.value || !searchText.value) return selectableGroups.value
  const q = searchText.value.toLowerCase()
  return selectableGroups.value.filter(
    (g) => g.name.toLowerCase().includes(q) || g.description?.toLowerCase().includes(q)
  )
})

// id -> 分组元数据，`groups` 优先于 `fallbackGroups`
const groupMetaById = computed(() => {
  const map = new Map<number, Group>()
  for (const g of props.fallbackGroups || []) map.set(g.id, g)
  for (const g of props.groups) map.set(g.id, g)
  return map
})

const knownGroupIdSet = computed(() => new Set(props.groups.map((g) => g.id)))

const hiddenSelected = computed(() =>
  selectedIds.value
    .filter((id) => !selectableIdSet.value.has(id))
    .map((id) => {
      const group = groupMetaById.value.get(id) || null
      let reason: string
      if (!knownGroupIdSet.value.has(id)) {
        reason = group
          ? t('common.groupSelector.reasonInactive')
          : t('common.groupSelector.reasonUnknown')
      } else {
        reason = t('common.groupSelector.reasonPlatformMismatch', {
          platform: group?.platform || ''
        })
      }
      return { id, group, reason }
    })
)

const handleChange = (groupId: number, checked: boolean) => {
  const current = selectedIds.value
  const newValue = checked
    ? current.includes(groupId)
      ? current
      : [...current, groupId]
    : current.filter((id) => id !== groupId)
  emit('update:modelValue', newValue)
}
</script>
