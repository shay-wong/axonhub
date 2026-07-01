import type { Channel } from './schema';

export type ChannelStatusKind = 'error' | 'temporaryDisable' | 'disabledKeys';
export type ChannelStatusActionID = 'resolveError' | 'clearTemporaryDisable' | 'manageDisabledKeys';
export type ChannelStatusTone = 'destructive' | 'temporaryDisable' | 'disabledKeys';
export type ChannelStatusTooltipKind = 'error' | 'temporaryDisable' | 'disabledKeys' | 'disabledKeysReadOnly';

export type ChannelStatusPolicyItem = {
  kind: ChannelStatusKind;
  tone: ChannelStatusTone;
  actionID?: ChannelStatusActionID;
};

export type ChannelStatusActionablePolicyItem = ChannelStatusPolicyItem & {
  actionID: ChannelStatusActionID;
};

export type ChannelStatusPolicy = {
  canWrite: boolean;
  hasError: boolean;
  hasDisabledKeys: boolean;
  isTemporarilyDisabled: boolean;
  disabledKeysCount: number;
  statusItems: ChannelStatusPolicyItem[];
  primaryItem: ChannelStatusPolicyItem | null;
  menuItems: ChannelStatusActionablePolicyItem[];
};

export type ChannelStatusPolicyOptions = {
  canWrite: boolean;
};

export type ChannelStatusViewAction = {
  id: ChannelStatusActionID;
  quickLabelKey: string;
  disabled: boolean;
  pending: boolean;
};

export type ChannelStatusViewItem = {
  kind: ChannelStatusKind;
  tone: ChannelStatusTone;
  tooltipKind: ChannelStatusTooltipKind;
  action?: ChannelStatusViewAction;
};

export type ChannelStatusViewMenuItem = ChannelStatusViewItem & {
  action: ChannelStatusViewAction;
};

export type ChannelStatusViewModel = {
  statusItems: ChannelStatusViewItem[];
  primaryItem: ChannelStatusViewItem | null;
  menuItems: ChannelStatusViewMenuItem[];
};

export type ChannelStatusViewModelOptions = {
  clearTemporaryDisablePending?: boolean;
};

export function isChannelTemporarilyDisabled(channel: Pick<Channel, 'temporaryDisabledUntil'>): boolean {
  if (!channel.temporaryDisabledUntil) return false;
  const until = new Date(channel.temporaryDisabledUntil).getTime();
  return Number.isFinite(until) && until > Date.now();
}

export function getChannelStatusPolicy(channel: Channel, { canWrite }: ChannelStatusPolicyOptions): ChannelStatusPolicy {
  const hasError = !!channel.errorMessage;
  const disabledKeysCount = channel.disabledAPIKeys?.length ?? 0;
  const hasDisabledKeys = disabledKeysCount > 0;
  const temporarilyDisabled = isChannelTemporarilyDisabled(channel);

  const errorItem: ChannelStatusPolicyItem | null = hasError
    ? {
        kind: 'error',
        tone: 'destructive',
        actionID: canWrite ? 'resolveError' : undefined,
      }
    : null;

  const temporaryDisableItem: ChannelStatusPolicyItem | null = temporarilyDisabled
    ? {
        kind: 'temporaryDisable',
        tone: 'temporaryDisable',
        actionID: canWrite ? 'clearTemporaryDisable' : undefined,
      }
    : null;

  const disabledKeysItem: ChannelStatusPolicyItem | null = hasDisabledKeys
    ? {
        kind: 'disabledKeys',
        tone: 'disabledKeys',
        actionID: canWrite ? 'manageDisabledKeys' : undefined,
      }
    : null;

  const orderedItems = [errorItem, temporaryDisableItem, disabledKeysItem].filter((item): item is ChannelStatusPolicyItem => !!item);

  return {
    canWrite,
    hasError,
    hasDisabledKeys,
    isTemporarilyDisabled: temporarilyDisabled,
    disabledKeysCount,
    statusItems: orderedItems,
    primaryItem: orderedItems[0] ?? null,
    menuItems: orderedItems.filter(hasActionID),
  };
}

export function getChannelStatusViewModel(
  policy: ChannelStatusPolicy,
  { clearTemporaryDisablePending = false }: ChannelStatusViewModelOptions = {}
): ChannelStatusViewModel {
  const toViewItem = (item: ChannelStatusPolicyItem): ChannelStatusViewItem => ({
    kind: item.kind,
    tone: item.tone,
    tooltipKind: getChannelStatusTooltipKind(item, policy),
    action: item.actionID ? getChannelStatusViewAction(item.actionID, { clearTemporaryDisablePending }) : undefined,
  });

  const toViewMenuItem = (item: ChannelStatusActionablePolicyItem): ChannelStatusViewMenuItem => ({
    ...toViewItem(item),
    action: getChannelStatusViewAction(item.actionID, { clearTemporaryDisablePending }),
  });

  const primaryItem = policy.primaryItem ? toViewItem(policy.primaryItem) : null;

  return {
    statusItems: policy.statusItems.map(toViewItem),
    primaryItem,
    menuItems: policy.menuItems.map(toViewMenuItem),
  };
}

function hasActionID(item: ChannelStatusPolicyItem): item is ChannelStatusActionablePolicyItem {
  return !!item.actionID;
}

function getChannelStatusViewAction(
  actionID: ChannelStatusActionID,
  { clearTemporaryDisablePending }: Required<ChannelStatusViewModelOptions>
): ChannelStatusViewAction {
  switch (actionID) {
    case 'resolveError':
      return {
        id: actionID,
        quickLabelKey: 'channels.actions.markErrorResolved',
        disabled: false,
        pending: false,
      };
    case 'clearTemporaryDisable':
      return {
        id: actionID,
        quickLabelKey: 'channels.actions.quickClearTemporaryDisable',
        disabled: clearTemporaryDisablePending,
        pending: clearTemporaryDisablePending,
      };
    case 'manageDisabledKeys':
      return {
        id: actionID,
        quickLabelKey: 'channels.actions.quickManageDisabledKeys',
        disabled: false,
        pending: false,
      };
  }
}

function getChannelStatusTooltipKind(item: ChannelStatusPolicyItem, policy: ChannelStatusPolicy): ChannelStatusTooltipKind {
  switch (item.kind) {
    case 'error':
      return 'error';
    case 'temporaryDisable':
      return 'temporaryDisable';
    case 'disabledKeys':
      return policy.canWrite ? 'disabledKeys' : 'disabledKeysReadOnly';
  }
}
