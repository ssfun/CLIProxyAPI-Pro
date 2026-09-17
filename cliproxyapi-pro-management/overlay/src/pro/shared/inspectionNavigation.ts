export interface InspectionFocusTarget {
  authId?: string;
  authIndex?: string;
  fileName?: string;
}

export interface InspectionFocusLocationState {
  inspectionFocus: InspectionFocusTarget;
}

export const buildInspectionFocusLocationState = (
  target: InspectionFocusTarget,
): InspectionFocusLocationState => ({
  inspectionFocus: {
    ...(target.authId?.trim() ? { authId: target.authId.trim() } : {}),
    ...(target.authIndex?.trim() ? { authIndex: target.authIndex.trim() } : {}),
    ...(target.fileName?.trim() ? { fileName: target.fileName.trim() } : {}),
  },
});

export const readInspectionFocusLocationState = (
  value: unknown,
): InspectionFocusTarget | null => {
  if (!value || typeof value !== 'object') return null;
  const state = value as { inspectionFocus?: unknown };
  if (!state.inspectionFocus || typeof state.inspectionFocus !== 'object') return null;
  const target = state.inspectionFocus as Record<string, unknown>;
  const authId = typeof target.authId === 'string' ? target.authId.trim() : '';
  const authIndex = typeof target.authIndex === 'string' ? target.authIndex.trim() : '';
  const fileName = typeof target.fileName === 'string' ? target.fileName.trim() : '';
  if (!authId && !authIndex && !fileName) return null;
  return {
    ...(authId ? { authId } : {}),
    ...(authIndex ? { authIndex } : {}),
    ...(fileName ? { fileName } : {}),
  };
};
