import assert from 'node:assert/strict';
import test from 'node:test';
import { useAnalyticsFilterStore } from './analyticsStore.ts';

test('default and reset ranges cover 30 local calendar days with explicit timestamps', (t) => {
  const now = new Date(2026, 10, 10, 12, 34, 56);
  t.mock.timers.enable({ apis: ['Date'], now });

  useAnalyticsFilterStore.getState().resetFilter();
  const { filter } = useAnalyticsFilterStore.getState();
  assert.equal(filter.startTime, new Date(2026, 9, 12, 0, 0, 0, 0).toISOString());
  assert.equal(filter.endTime, new Date(2026, 10, 10, 23, 59, 59, 999).toISOString());
  assert.match(filter.startTime, /T.*Z$/);
  assert.match(filter.endTime, /T.*Z$/);

  useAnalyticsFilterStore.getState().setStartTime(null);
  t.mock.timers.setTime(new Date(2026, 10, 11, 12, 34, 56).getTime());
  useAnalyticsFilterStore.getState().resetFilter();
  assert.equal(useAnalyticsFilterStore.getState().filter.startTime, new Date(2026, 9, 13).toISOString());
});
