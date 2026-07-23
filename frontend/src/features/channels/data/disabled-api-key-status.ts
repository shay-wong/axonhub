export type ActiveDisabledAPIKeyStatus =
  | { kind: 'permanent' }
  | { kind: 'temporary'; disabledUntilMs: number; remainingSeconds: number };

export function getActiveDisabledAPIKeyStatus(
  disabledUntil: string | null | undefined,
  nowMs = Date.now()
): ActiveDisabledAPIKeyStatus | null {
  if (!disabledUntil) return { kind: 'permanent' };

  const disabledUntilMs = Date.parse(disabledUntil);
  if (!Number.isFinite(disabledUntilMs)) return null;

  const remainingSeconds = Math.ceil((disabledUntilMs - nowMs) / 1000);
  if (remainingSeconds <= 0) return null;

  return { kind: 'temporary', disabledUntilMs, remainingSeconds };
}

export function formatDisabledAPIKeyCountdown(remainingSeconds: number): string {
  const hours = Math.floor(remainingSeconds / 3600);
  const minutes = Math.floor((remainingSeconds % 3600) / 60);
  const seconds = remainingSeconds % 60;

  return [hours, minutes, seconds].map((part) => String(part).padStart(2, '0')).join(':');
}
