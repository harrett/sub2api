// Anchor catalogs for custom menu item placement (System Settings > Custom Menu).
// Each entry mirrors a fixed nav item's `path` + i18n `nav.*` label key so the
// admin UI can offer "insert after <built-in item>" as a dropdown.
//
// Keep in sync with the fixed item lists in AppSidebar.vue:
// - USER_MENU_ANCHORS mirrors buildSelfNavItems() (used for visibility: 'user')
// - ADMIN_MENU_ANCHORS mirrors adminNavItems() (used for visibility: 'admin')
// A stale/unmatched anchor_path is harmless: the sidebar falls back to
// appending the item at the end.

export interface CustomMenuAnchorOption {
  path: string
  labelKey: string
}

export const USER_MENU_ANCHORS: CustomMenuAnchorOption[] = [
  { path: '/dashboard', labelKey: 'nav.dashboard' },
  { path: '/keys', labelKey: 'nav.apiKeys' },
  { path: '/batch-image', labelKey: 'nav.batchImage' },
  { path: '/usage', labelKey: 'nav.usage' },
  { path: '/available-channels', labelKey: 'nav.availableChannels' },
  { path: '/monitor', labelKey: 'nav.channelStatus' },
  { path: '/subscriptions', labelKey: 'nav.mySubscriptions' },
  { path: '/purchase', labelKey: 'nav.buySubscription' },
  { path: '/orders', labelKey: 'nav.myOrders' },
  { path: '/redeem', labelKey: 'nav.redeem' },
  { path: '/affiliate', labelKey: 'nav.affiliate' },
  { path: '/profile', labelKey: 'nav.profile' }
]

export const ADMIN_MENU_ANCHORS: CustomMenuAnchorOption[] = [
  { path: '/admin/dashboard', labelKey: 'nav.dashboard' },
  { path: '/admin/ops', labelKey: 'nav.ops' },
  { path: '/admin/users', labelKey: 'nav.users' },
  { path: '/admin/groups', labelKey: 'nav.groups' },
  { path: '/admin/channels', labelKey: 'nav.channelManagement' },
  { path: '/admin/subscriptions', labelKey: 'nav.subscriptions' },
  { path: '/admin/accounts', labelKey: 'nav.accounts' },
  { path: '/admin/plugins', labelKey: 'nav.plugins' },
  { path: '/admin/announcements', labelKey: 'nav.announcements' },
  { path: '/admin/proxies', labelKey: 'nav.proxies' },
  { path: '/admin/security-audit', labelKey: 'nav.securityAudit' },
  { path: '/admin/redeem', labelKey: 'nav.redeemCodes' },
  { path: '/admin/promo-codes', labelKey: 'nav.promoCodes' },
  { path: '/admin/affiliates', labelKey: 'nav.affiliateManagement' },
  { path: '/admin/orders', labelKey: 'nav.orderManagement' },
  { path: '/admin/usage', labelKey: 'nav.usage' },
  { path: '/admin/audit-logs', labelKey: 'nav.auditLogs' },
  { path: '/keys', labelKey: 'nav.apiKeys' },
  { path: '/admin/settings', labelKey: 'nav.settings' }
]
