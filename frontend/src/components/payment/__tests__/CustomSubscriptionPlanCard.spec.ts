import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import type { SubscriptionPlan } from '@/types/payment'
import CustomSubscriptionPlanCard from '../CustomSubscriptionPlanCard.vue'

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  messages: {
    en: {
      payment: {
        days: 'days',
        weeks: 'weeks',
        months: 'months',
        perMonth: 'month',
        subscribeNow: 'Choose plan',
        renewNow: 'Renew plan',
        planCard: {
          dailyLimit: 'Daily limit',
          monthlyLimit: 'Monthly limit',
          unlimited: 'Unlimited',
        },
      },
    },
  },
})

const plan: SubscriptionPlan = {
  id: 1,
  group_id: 10,
  group_platform: 'openai',
  name: 'Codex Pro',
  description: 'For everyday development',
  price: 168,
  rate_multiplier: 2,
  daily_limit_usd: 20,
  weekly_limit_usd: 80,
  monthly_limit_usd: 220,
  validity_days: 30,
  validity_unit: 'day',
  features: ['Priority access'],
  for_sale: true,
  sort_order: 1,
}

describe('CustomSubscriptionPlanCard', () => {
  it('shows daily and monthly limits without the rate or weekly limit', () => {
    const wrapper = mount(CustomSubscriptionPlanCard, {
      props: { plan },
      global: { plugins: [i18n] },
    })

    expect(wrapper.text()).toContain('payment.planCard.dailyLimit')
    expect(wrapper.text()).toContain('payment.planCard.monthlyLimit')
    expect(wrapper.text()).not.toContain('payment.planCard.rate')
    expect(wrapper.text()).not.toContain('payment.planCard.weeklyLimit')
    expect(wrapper.findAll('.purchase-plan-card__limits > div')).toHaveLength(2)
  })

  it('emits the selected plan from its redesigned action', async () => {
    const wrapper = mount(CustomSubscriptionPlanCard, {
      props: { plan },
      global: { plugins: [i18n] },
    })

    await wrapper.find('.purchase-plan-card__action').trigger('click')

    expect(wrapper.emitted('select')).toEqual([[plan]])
  })
})
