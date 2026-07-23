<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">Cross-platform subscriptions</h1>
          <p class="text-sm text-gray-500 dark:text-gray-400">Configure shared USD plans and review user contracts.</p>
        </div>
        <div class="flex gap-2">
          <button class="btn" :class="tab === 'plans' ? 'btn-primary' : 'btn-secondary'" @click="tab = 'plans'">Plans</button>
          <button class="btn" :class="tab === 'contracts' ? 'btn-primary' : 'btn-secondary'" @click="tab = 'contracts'; loadContracts()">Contracts</button>
        </div>
      </div>

      <div v-if="!bundleEnabled" class="card p-6 text-sm text-gray-600 dark:text-gray-300">
        The bundle subscription feature flag is disabled. Enable it in Settings before managing plans.
      </div>
      <div v-else-if="loading" class="flex justify-center py-16"><div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" /></div>
      <template v-else-if="tab === 'plans'">
        <div class="flex justify-end"><button class="btn btn-primary" @click="openPlanEditor()">New plan</button></div>
        <form v-if="planEditorOpen" class="card grid gap-4 p-5" @submit.prevent="savePlan">
          <div class="grid gap-4 md:grid-cols-3">
            <label class="input-label">Name<input v-model="planForm.name" class="input" required /></label>
            <label class="input-label">Product<input v-model="planForm.product_name" class="input" /></label>
            <label class="input-label">Price (USD)<input v-model.number="planForm.price" class="input" type="number" min="0.01" step="0.01" required /></label>
            <label class="input-label">Validity days<input v-model.number="planForm.validity_days" class="input" type="number" min="1" required /></label>
            <label class="input-label">Shared daily USD<input v-model.number="planForm.shared_daily_limit_usd" class="input" type="number" min="0" step="0.01" /></label>
            <label class="input-label">Shared monthly USD<input v-model.number="planForm.shared_monthly_limit_usd" class="input" type="number" min="0" step="0.01" /></label>
          </div>
          <label class="input-label">Description<textarea v-model="planForm.description" class="input min-h-20" /></label>
          <div>
            <p class="input-label">Subscription groups and optional platform caps</p>
            <div class="grid gap-2 md:grid-cols-2">
              <div v-for="group in groupOptions" :key="group.id" class="flex flex-wrap items-center gap-2 rounded border border-gray-200 p-2 dark:border-dark-700">
                <label class="flex min-w-48 items-center gap-2 text-sm"><input v-model="selectedGroupIDs" type="checkbox" :value="group.id" />{{ group.name }} · {{ group.platform }} (#{{ group.id }})</label>
                <input v-model="groupLimits[group.id].daily" class="input w-28" type="number" min="0" step="0.01" placeholder="daily cap" :disabled="!selectedGroupIDs.includes(group.id)" />
                <input v-model="groupLimits[group.id].monthly" class="input w-28" type="number" min="0" step="0.01" placeholder="monthly cap" :disabled="!selectedGroupIDs.includes(group.id)" />
              </div>
            </div>
          </div>
          <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" @click="planEditorOpen = false">Cancel</button><button type="submit" class="btn btn-primary" :disabled="saving">{{ editingPlanID ? 'Save changes' : 'Create plan' }}</button></div>
        </form>
        <div v-if="plans.length === 0" class="card p-8 text-center text-gray-500">No bundle plans configured.</div>
        <div v-else class="grid gap-4 lg:grid-cols-2">
          <div v-for="plan in plans" :key="plan.id" class="card p-5">
            <div class="flex items-start justify-between gap-3">
              <div><h2 class="font-semibold text-gray-900 dark:text-white">{{ plan.name }}</h2><p class="text-xs text-gray-500">{{ plan.product_name }}</p></div>
              <span class="badge" :class="plan.for_sale ? 'badge-success' : 'badge-warning'">{{ plan.for_sale ? 'On sale' : 'Hidden' }}</span>
            </div>
            <p class="mt-3 text-sm text-gray-600 dark:text-gray-300">{{ plan.description }}</p>
            <div class="mt-4 grid grid-cols-2 gap-3 text-sm"><span>Price: {{ plan.currency }} {{ plan.price.toFixed(2) }}</span><span>Validity: {{ plan.validity_days }} {{ plan.validity_unit }}</span><span>Daily shared: {{ formatLimit(plan.shared_daily_limit_usd) }}</span><span>Monthly shared: {{ formatLimit(plan.shared_monthly_limit_usd) }}</span></div>
              <div class="mt-4 flex flex-wrap gap-2 text-xs text-gray-500"><span v-for="group in plan.groups || []" :key="group.group_id" class="rounded-full bg-gray-100 px-2 py-1 dark:bg-dark-700">{{ group.platform || group.group_name || `Group #${group.group_id}` }}</span></div>
              <div class="mt-4 flex justify-end gap-2"><button class="btn btn-secondary text-xs" @click="openPlanEditor(plan)">Edit</button><button class="btn btn-secondary text-xs" @click="removePlan(plan.id)">Delete</button></div>
            </div>
        </div>
      </template>
      <template v-else>
        <form class="card grid gap-3 p-5 md:grid-cols-4" @submit.prevent="assignContract">
          <label class="input-label">User ID<input v-model.number="assignment.user_id" class="input" type="number" min="1" required /></label>
          <label class="input-label">Plan<select v-model.number="assignment.plan_id" class="input" required><option :value="0" disabled>Select plan</option><option v-for="plan in plans" :key="plan.id" :value="plan.id">{{ plan.name }} (#{{ plan.id }})</option></select></label>
          <label class="input-label">Days (optional)<input v-model.number="assignment.days" class="input" type="number" min="1" /></label>
          <div class="flex items-end"><button class="btn btn-primary w-full" type="submit" :disabled="assigning">Assign contract</button></div>
        </form>
        <div v-if="contracts.length === 0" class="card p-8 text-center text-gray-500">No bundle contracts found.</div>
        <div v-else class="overflow-x-auto rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
          <table class="min-w-full text-left text-sm"><thead class="border-b border-gray-200 text-xs uppercase text-gray-500 dark:border-dark-700"><tr><th class="px-4 py-3">Contract</th><th class="px-4 py-3">User</th><th class="px-4 py-3">Status</th><th class="px-4 py-3">Usage</th><th class="px-4 py-3 text-right">Actions</th></tr></thead><tbody><tr v-for="contract in contracts" :key="contract.id" class="border-b border-gray-100 last:border-0 dark:border-dark-700"><td class="px-4 py-3">#{{ contract.id }} · {{ contract.plan?.name || `Plan #${contract.bundle_plan_id}` }}</td><td class="px-4 py-3">#{{ contract.user_id }}</td><td class="px-4 py-3">{{ contract.status }}</td><td class="px-4 py-3">${{ contract.daily_usage_usd.toFixed(2) }} / ${{ contract.monthly_usage_usd.toFixed(2) }}</td><td class="px-4 py-3 text-right"><button class="btn btn-secondary text-xs" @click="revoke(contract.id)">Revoke</button><button class="btn btn-secondary ml-2 text-xs" @click="reset(contract.id)">Reset usage</button></td></tr></tbody></table>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { adminBundleSubscriptionsAPI } from '@/api/admin/bundleSubscriptions'
import type { BundlePlan, BundleSubscription } from '@/api/bundleSubscriptions'
import { groupsAPI } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'

const bundleEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.bundleSubscriptions))
const tab = ref<'plans' | 'contracts'>('plans')
const loading = ref(true)
const plans = ref<BundlePlan[]>([])
const contracts = ref<BundleSubscription[]>([])
const groupOptions = ref<AdminGroup[]>([])
const planEditorOpen = ref(false)
const editingPlanID = ref<number | null>(null)
const saving = ref(false)
const selectedGroupIDs = ref<number[]>([])
const groupLimits = reactive<Record<number, { daily: number | null; monthly: number | null }>>({})
const planForm = reactive({ name: '', description: '', product_name: '', price: 0, validity_days: 30, shared_daily_limit_usd: null as number | null, shared_monthly_limit_usd: null as number | null })
const assigning = ref(false)
const assignment = reactive({ user_id: 0, plan_id: 0, days: undefined as number | undefined })

function formatLimit(value: number | null | undefined) { return value == null ? 'Unlimited' : `$${value.toFixed(2)}` }
async function loadPlans() { plans.value = (await adminBundleSubscriptionsAPI.getPlans()).data }
async function loadContracts() { contracts.value = (await adminBundleSubscriptionsAPI.getUserSubscriptions()).data }
async function revoke(id: number) { await adminBundleSubscriptionsAPI.revoke(id); await loadContracts() }
async function reset(id: number) { await adminBundleSubscriptionsAPI.resetUsage(id); await loadContracts() }
function openPlanEditor(plan?: BundlePlan) {
  editingPlanID.value = plan?.id ?? null
  planForm.name = plan?.name ?? ''
  planForm.description = plan?.description ?? ''
  planForm.product_name = plan?.product_name ?? ''
  planForm.price = plan?.price ?? 0
  planForm.validity_days = plan?.validity_days ?? 30
  planForm.shared_daily_limit_usd = plan?.shared_daily_limit_usd ?? null
  planForm.shared_monthly_limit_usd = plan?.shared_monthly_limit_usd ?? null
  selectedGroupIDs.value = plan?.groups?.map((group) => group.group_id) ?? []
  groupOptions.value.forEach((group) => {
    const current = plan?.groups?.find((item) => item.group_id === group.id)
    groupLimits[group.id] = { daily: current?.daily_limit_usd ?? null, monthly: current?.monthly_limit_usd ?? null }
  })
  planEditorOpen.value = true
}
function planPayload() {
  return {
    ...planForm,
    currency: 'USD', validity_unit: 'day', features: '', for_sale: true, sort_order: 0,
    groups: selectedGroupIDs.value.map((id) => ({ group_id: id, daily_limit_usd: groupLimits[id]?.daily ?? null, monthly_limit_usd: groupLimits[id]?.monthly ?? null })),
  }
}
async function savePlan() {
  if (selectedGroupIDs.value.length === 0) return
  saving.value = true
  try {
    const payload = planPayload()
    if (editingPlanID.value) await adminBundleSubscriptionsAPI.updatePlan(editingPlanID.value, payload)
    else await adminBundleSubscriptionsAPI.createPlan(payload)
    await loadPlans(); planEditorOpen.value = false
  } finally { saving.value = false }
}
async function removePlan(id: number) {
  if (window.confirm('Delete this bundle plan?')) { await adminBundleSubscriptionsAPI.deletePlan(id); await loadPlans() }
}
async function assignContract() {
  if (!assignment.user_id || !assignment.plan_id) return
  assigning.value = true
  try { await adminBundleSubscriptionsAPI.assign(assignment.user_id, assignment.plan_id, assignment.days); assignment.user_id = 0; assignment.plan_id = 0; assignment.days = undefined; await loadContracts() } finally { assigning.value = false }
}
onMounted(async () => {
  if (bundleEnabled.value) {
    const [planResult, groups] = await Promise.all([adminBundleSubscriptionsAPI.getPlans(), groupsAPI.getAll()])
    plans.value = planResult.data
    groupOptions.value = groups.filter((group) => group.status === 'active' && group.subscription_type === 'subscription')
    groupOptions.value.forEach((group) => { groupLimits[group.id] = { daily: null, monthly: null } })
  }
  loading.value = false
})
</script>
