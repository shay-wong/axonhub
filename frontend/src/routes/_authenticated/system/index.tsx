import { createFileRoute, redirect } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import SystemManagement from '@/features/system';
import type { SystemTabKey } from '@/features/system/components/tabs';

const LEGACY_SYSTEM_TAB_SECTIONS = {
  retry: 'requests',
  proxy: 'requests',
  quota: 'requests',
  webhook: 'integrations',
  'claude-code': 'integrations',
  codex: 'integrations',
  storage: 'operations',
  diagnostics: 'operations',
  backup: 'operations',
} as const;

export function getLegacySystemTabRedirect(tab: string | undefined) {
  if (!tab || !Object.prototype.hasOwnProperty.call(LEGACY_SYSTEM_TAB_SECTIONS, tab)) {
    return null;
  }
  const legacyTab = tab as keyof typeof LEGACY_SYSTEM_TAB_SECTIONS;
  return { section: LEGACY_SYSTEM_TAB_SECTIONS[legacyTab], tab: legacyTab };
}

function ProtectedSystem() {
  const search = Route.useSearch();

  return (
    <RouteGuard requiredScopes={['read_settings']} scopeLevel="system">
      <SystemManagement initialTab={search.tab as SystemTabKey | undefined} />
    </RouteGuard>
  );
}

export const Route = createFileRoute('/_authenticated/system/')({
  beforeLoad: ({ search }) => {
    const legacy = getLegacySystemTabRedirect(search.tab);
    if (legacy) {
      throw redirect({ to: '/system/$section', params: { section: legacy.section }, search: { tab: legacy.tab }, replace: true });
    }
  },
  component: ProtectedSystem,
  validateSearch: (search: { tab?: SystemTabKey }) => search,
});
