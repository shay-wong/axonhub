import { create } from 'zustand';
import type { AnalyticsFilter } from '@/features/analytics/data/analytics';

interface AnalyticsFilterState {
  filter: AnalyticsFilter;
  setStartTime: (time: string | null) => void;
  setEndTime: (time: string | null) => void;
  setProjectIDs: (ids: string[]) => void;
  setChannelIDs: (ids: string[]) => void;
  setModelIDs: (ids: string[]) => void;
  setAPIKeyIDs: (ids: string[]) => void;
  setUserIDs: (ids: string[]) => void;
  resetFilter: () => void;
}

function formatLocalDate(date: Date): string {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

function createDefaultFilter(): AnalyticsFilter {
  const today = new Date();
  const startDate = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 29);

  return {
    startTime: formatLocalDate(startDate),
    endTime: formatLocalDate(today),
    projectIDs: undefined,
    channelIDs: undefined,
    modelIDs: undefined,
    apiKeyIDs: undefined,
    userIDs: undefined,
  };
}

export const useAnalyticsFilterStore = create<AnalyticsFilterState>((set) => ({
  filter: createDefaultFilter(),

  setStartTime: (time) =>
    set((state) => ({
      filter: { ...state.filter, startTime: time },
    })),

  setEndTime: (time) =>
    set((state) => ({
      filter: { ...state.filter, endTime: time },
    })),

  setProjectIDs: (ids) =>
    set((state) => ({
      filter: { ...state.filter, projectIDs: ids.length > 0 ? ids : undefined },
    })),

  setChannelIDs: (ids) =>
    set((state) => ({
      filter: { ...state.filter, channelIDs: ids.length > 0 ? ids : undefined },
    })),

  setModelIDs: (ids) =>
    set((state) => ({
      filter: { ...state.filter, modelIDs: ids.length > 0 ? ids : undefined },
    })),

  setAPIKeyIDs: (ids) =>
    set((state) => ({
      filter: { ...state.filter, apiKeyIDs: ids.length > 0 ? ids : undefined },
    })),

  setUserIDs: (ids) =>
    set((state) => ({
      filter: { ...state.filter, userIDs: ids.length > 0 ? ids : undefined },
    })),

  resetFilter: () =>
    set({ filter: createDefaultFilter() }),
}));
