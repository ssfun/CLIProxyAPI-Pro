import type { ReactNode } from 'react';
import { monitoringModule } from '@/pro/modules/monitoring';
import { dataManagementModule } from '@/pro/modules/dataManagement';
import { inspectionModule } from '@/pro/modules/inspection';
import { apiKeyPolicyModule } from '@/pro/modules/apiKeyPolicy';
import { oauthPolicyModule } from '@/pro/modules/oauthPolicy';
import { proxyPoolModule } from '@/pro/modules/proxyPool';
import { quotaModule } from '@/pro/modules/quota';

export interface ProRouteEntry {
  path: string;
  element: ReactNode;
}

export interface ProNavigationItem {
  path: string;
  labelKey: string;
  metaKey: string;
  icon: ReactNode;
}

export interface ProNavigationGroup {
  id: string;
  labelKey: string;
  items: ProNavigationItem[];
}

// This is the only host-owned list. Route, navigation and bootstrap projections
// are derived from each module's public manifest.
const proModules = [
  monitoringModule,
  inspectionModule,
  apiKeyPolicyModule,
  oauthPolicyModule,
  proxyPoolModule,
  dataManagementModule,
  quotaModule,
];

export const proRoutes: ProRouteEntry[] = proModules.flatMap((module) =>
  [...(module.route ? [module.route] : []), ...(module.routes ?? [])]
);

const navigationGroups = new Map<string, ProNavigationGroup>();
for (const module of proModules) {
  const navigation = module.navigation;
  if (!navigation) continue;
  const group = navigationGroups.get(navigation.groupId) ?? {
    id: navigation.groupId,
    labelKey: navigation.groupLabelKey,
    items: [],
  };
  group.items.push({
    path: navigation.path,
    labelKey: navigation.labelKey,
    metaKey: navigation.metaKey,
    icon: navigation.icon,
  });
  navigationGroups.set(group.id, group);
}

export const proNavigationGroups: ProNavigationGroup[] = [...navigationGroups.values()];

export const proBootstraps = proModules.flatMap((module) =>
  module.bootstrap ? [{ id: module.id, element: module.bootstrap }] : []
);
