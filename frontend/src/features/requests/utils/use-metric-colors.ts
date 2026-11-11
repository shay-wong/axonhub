// 职责: 让请求列表和颜色编辑弹窗订阅同一份浏览器偏好。
// 关联文件:
//   frontend/src/features/requests/utils/metric-colors.ts
//   frontend/src/features/requests/components/metric-color-dialog.tsx
//   frontend/src/features/requests/components/requests-columns.tsx
import { useSyncExternalStore } from 'react';
import { getMetricColorSettingsSnapshot, subscribeMetricColors } from './metric-colors';

/**
 * Subscribes the request list to the stored palette so a change in the dialog
 * recolours the table immediately (and across tabs via the storage event).
 *
 * Kept in its own module so `metric-colors.ts` stays React-free and can be unit
 * tested by transpiling it directly.
 */
export function useMetricColorSettings() {
  return useSyncExternalStore(subscribeMetricColors, getMetricColorSettingsSnapshot, getMetricColorSettingsSnapshot);
}
