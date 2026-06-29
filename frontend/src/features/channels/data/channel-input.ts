import type { ChannelSettings, CreateChannelInput, UpdateChannelInput } from './schema';

type OpenCodeGoQuotaSettings = NonNullable<NonNullable<NonNullable<ChannelSettings>['providerQuota']>['opencodeGo']>;

export function sanitizeChannelSettingsForInput(settings: ChannelSettings | null | undefined): ChannelSettings | null | undefined {
  const opencodeGo = settings?.providerQuota?.opencodeGo;
  if (!settings?.providerQuota || !opencodeGo) {
    return settings;
  }

  const inputOpencodeGo = projectOpenCodeGoQuotaSettingsForInput(opencodeGo);
  if (Object.keys(inputOpencodeGo).length === Object.keys(opencodeGo).length) {
    return settings;
  }

  return {
    ...settings,
    providerQuota: {
      ...settings.providerQuota,
      opencodeGo: inputOpencodeGo,
    },
  };
}

export function sanitizeChannelMutationInput<T extends { settings?: ChannelSettings | null }>(input: T): T {
  const settings = sanitizeChannelSettingsForInput(input.settings);
  if (settings === input.settings) {
    return input;
  }

  return {
    ...input,
    settings,
  };
}

function projectOpenCodeGoQuotaSettingsForInput(opencodeGo: OpenCodeGoQuotaSettings): OpenCodeGoQuotaSettings {
  const inputOpencodeGo: OpenCodeGoQuotaSettings = {};

  if (Object.prototype.hasOwnProperty.call(opencodeGo, 'workspaceId')) {
    inputOpencodeGo.workspaceId = opencodeGo.workspaceId;
  }

  if (Object.prototype.hasOwnProperty.call(opencodeGo, 'authCookie')) {
    inputOpencodeGo.authCookie = opencodeGo.authCookie;
  }

  if (Object.prototype.hasOwnProperty.call(opencodeGo, 'clearAuthCookie')) {
    inputOpencodeGo.clearAuthCookie = opencodeGo.clearAuthCookie;
  }

  return inputOpencodeGo;
}

export function buildCreateChannelVariables(input: CreateChannelInput) {
  return {
    input: sanitizeChannelMutationInput(input),
  };
}

export function buildDuplicateChannelVariables(sourceID: string, input: CreateChannelInput) {
  return {
    sourceID,
    input: sanitizeChannelMutationInput(input),
  };
}

export function buildBulkCreateChannelsVariables<T extends { settings?: ChannelSettings | null }>(input: T) {
  return {
    input: sanitizeChannelMutationInput(input),
  };
}

export function buildUpdateChannelVariables(id: string, input: UpdateChannelInput) {
  return {
    id,
    input: sanitizeChannelMutationInput(input),
  };
}
