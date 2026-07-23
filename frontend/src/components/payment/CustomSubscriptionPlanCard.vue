<template>
  <article class="purchase-plan-card" :class="{ 'purchase-plan-card--renewal': isRenewal }">
    <div class="purchase-plan-card__topline" />

    <div class="purchase-plan-card__content">
      <div class="purchase-plan-card__header">
        <span class="purchase-plan-card__platform">{{ platformLabel }}</span>
        <span v-if="isRenewal" class="purchase-plan-card__renewal">{{ t('payment.renewNow') }}</span>
      </div>

      <div class="purchase-plan-card__title-block">
        <h3 class="purchase-plan-card__title" :title="plan.name">{{ plan.name }}</h3>
        <p v-if="plan.description" class="purchase-plan-card__description">{{ plan.description }}</p>
      </div>

      <div class="purchase-plan-card__price">
        <span class="purchase-plan-card__currency">{{ currency }}</span>
        <span class="purchase-plan-card__amount">{{ plan.price }}</span>
        <span v-if="plan.currency" class="purchase-plan-card__currency-code">{{ plan.currency }}</span>
      </div>
      <p class="purchase-plan-card__validity">{{ validitySuffix }}</p>

      <dl class="purchase-plan-card__limits">
        <div>
          <dt>{{ t('payment.planCard.dailyLimit') }}</dt>
          <dd>{{ dailyLimit }}</dd>
        </div>
        <div>
          <dt>{{ t('payment.planCard.monthlyLimit') }}</dt>
          <dd>{{ monthlyLimit }}</dd>
        </div>
      </dl>

      <ul v-if="plan.features.length" class="purchase-plan-card__features">
        <li v-for="feature in plan.features.slice(0, 3)" :key="feature">{{ feature }}</li>
      </ul>

      <button type="button" class="purchase-plan-card__action" @click="emit('select', plan)">
        {{ isRenewal ? t('payment.renewNow') : t('payment.subscribeNow') }}
      </button>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SubscriptionPlan } from '@/types/payment'
import type { UserSubscription } from '@/types'
import { currencySymbol } from '@/components/payment/currency'
import { planValiditySuffix } from '@/components/payment/validity'
import { platformLabel as getPlatformLabel } from '@/utils/platformColors'

const props = defineProps<{ plan: SubscriptionPlan; activeSubscriptions?: UserSubscription[] }>()
const emit = defineEmits<{ select: [plan: SubscriptionPlan] }>()
const { t } = useI18n()

const isRenewal = computed(() =>
  props.activeSubscriptions?.some(subscription =>
    subscription.group_id === props.plan.group_id && subscription.status === 'active',
  ) ?? false,
)
const currency = computed(() => currencySymbol(props.plan.currency || 'USD'))
const platformLabel = computed(() => getPlatformLabel(props.plan.group_platform || ''))
const validitySuffix = computed(() => planValiditySuffix(props.plan, t))
const dailyLimit = computed(() => formatLimit(props.plan.daily_limit_usd))
const monthlyLimit = computed(() => formatLimit(props.plan.monthly_limit_usd))

function formatLimit(limit: number | null | undefined): string {
  return limit == null ? t('payment.planCard.unlimited') : `$${limit}`
}
</script>

<style scoped>
.purchase-plan-card {
  position: relative;
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: color-mix(in srgb, var(--surface-1) 94%, transparent);
  box-shadow: 0 10px 28px rgba(0, 0, 0, 0.12);
  transition: border-color 160ms ease, box-shadow 160ms ease, transform 160ms ease;
}

.purchase-plan-card:hover {
  transform: translateY(-3px);
  border-color: color-mix(in srgb, var(--accent) 62%, var(--line));
  box-shadow: 0 15px 34px rgba(0, 0, 0, 0.19);
}

.purchase-plan-card__topline {
  height: 5px;
  background: linear-gradient(90deg, var(--accent), #62d9b0);
}

.purchase-plan-card__content {
  display: flex;
  min-height: 330px;
  flex-direction: column;
  padding: 20px;
}

.purchase-plan-card__header {
  display: flex;
  min-height: 24px;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.purchase-plan-card__platform,
.purchase-plan-card__renewal {
  width: fit-content;
  border: 1px solid color-mix(in srgb, var(--accent) 30%, transparent);
  border-radius: 999px;
  background: var(--accent-dim);
  color: var(--accent);
  font-size: 11px;
  font-weight: 650;
  line-height: 1;
  padding: 6px 9px;
}

.purchase-plan-card__renewal {
  color: var(--text-dim);
}

.purchase-plan-card__title-block {
  margin-top: 18px;
  min-height: 70px;
}

.purchase-plan-card__title {
  overflow: hidden;
  color: var(--text);
  font-size: 19px;
  font-weight: 700;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.purchase-plan-card__description {
  display: -webkit-box;
  margin-top: 6px;
  overflow: hidden;
  color: var(--text-dim);
  font-size: 13px;
  line-height: 1.5;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.purchase-plan-card__price {
  display: flex;
  align-items: baseline;
  justify-content: center;
  margin-top: 8px;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.purchase-plan-card__currency {
  margin-right: 5px;
  color: var(--text-dim);
  font-size: 15px;
}

.purchase-plan-card__amount {
  font-size: 42px;
  font-weight: 700;
  letter-spacing: 0;
  line-height: 1;
}

.purchase-plan-card__currency-code {
  margin-left: 6px;
  color: var(--text-faint);
  font-size: 11px;
  font-weight: 600;
}

.purchase-plan-card__validity {
  margin-top: 7px;
  color: var(--text-dim);
  font-size: 12px;
  text-align: center;
}

.purchase-plan-card__limits {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--line);
}

.purchase-plan-card__limits div { min-width: 0; }
.purchase-plan-card__limits dt {
  overflow: hidden;
  color: var(--text-faint);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.purchase-plan-card__limits dd {
  margin-top: 4px;
  overflow: hidden;
  color: var(--text);
  font-size: 15px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.purchase-plan-card__features {
  display: grid;
  gap: 5px;
  margin: 16px 0 0;
  padding: 0;
  color: var(--text-dim);
  font-size: 12px;
  line-height: 1.4;
  list-style: none;
}
.purchase-plan-card__features li {
  overflow: hidden;
  padding-left: 12px;
  position: relative;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.purchase-plan-card__features li::before {
  position: absolute;
  top: 6px;
  left: 0;
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: var(--accent);
  content: '';
}

.purchase-plan-card__action {
  width: 100%;
  min-height: 42px;
  margin-top: auto;
  padding: 9px 12px;
  border: 1px solid color-mix(in srgb, var(--accent) 75%, transparent);
  border-radius: 6px;
  background: transparent;
  color: var(--accent);
  cursor: pointer;
  font: inherit;
  font-size: 14px;
  font-weight: 700;
  transition: background-color 160ms ease, color 160ms ease, box-shadow 160ms ease;
}

.purchase-plan-card__action:hover {
  background: var(--accent);
  box-shadow: 0 0 18px var(--accent-glow);
  color: var(--accent-ink);
}

.purchase-plan-card__action:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 3px;
}

@media (max-width: 639px) {
  .purchase-plan-card__content { min-height: 304px; padding: 18px; }
  .purchase-plan-card__amount { font-size: 38px; }
}
</style>
