import type { GroupPlatform } from '@/types'

export type KeyGroupProvider = 'anthropic' | 'openai' | 'other'

export const KEY_GROUP_PROVIDERS = ['anthropic', 'openai', 'other'] as const

// Classify by the configured upstream platform, never by a group's display name.
const PROVIDER_BY_PLATFORM: Record<GroupPlatform, KeyGroupProvider> = {
  anthropic: 'anthropic',
  openai: 'openai',
  kimi: 'other',
  zhipu: 'other',
  deepseek: 'other',
  minimax: 'other',
  gemini: 'other',
  grok: 'other',
  antigravity: 'other',
  composite: 'other',
  opencode_go: 'other',
  typesafe: 'other',
  command_code: 'other',
  cline: 'other'
}

export function getKeyGroupProvider(platform: GroupPlatform): KeyGroupProvider {
  return PROVIDER_BY_PLATFORM[platform] ?? 'other'
}

// Collections use representative provider marks rather than an invented brand logo.
export const KEY_GROUP_PROVIDER_ICONS: Record<KeyGroupProvider, GroupPlatform[]> = {
  anthropic: ['anthropic'],
  openai: ['openai'],
  other: ['gemini', 'grok']
}
